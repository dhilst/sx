// Command sx measures a Go program as |AST| - the number of nodes it takes to
// express - and shrinks it without changing what it does.
//
// The measure has no parameters, ignores formatting and comments, and is
// additive: a node costs one wherever it sits. "sx refactor" removes dead
// code, inlines abstractions used once, and factors out duplication, keeping
// only the changes that survive a build, the tests of every package that could
// be affected, and a re-count.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/build"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/pprof"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/dhilst/sx/internal/cost"
	"github.com/dhilst/sx/internal/refactor"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "sx:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) > 0 && args[0] == "refactor" {
		var args []string = args[1:]
		fs := flag.NewFlagSet("refactor", flag.ContinueOnError)
		fs.SetOutput(stderr)
		var egPaths pathListFlag
		apply := fs.Bool("apply", false, "write changes; without it, list what would be tried")
		check := fs.Bool("check", false, "exit non-zero at the first shrinking candidate; never writes to the tree")
		rounds := fs.Int("n", 10, "how many changes to attempt")
		runTests := fs.Bool("test", true, "run the tests after each change and revert if they fail")
		cpuprofile := fs.String("cpuprofile", "", "write a CPU profile of the run to this file")
		fs.Var(&egPaths, "eg", "file or directory of eg templates; repeat or separate with commas/path-list separators (empty disables eg)")
		if err := fs.Parse(args); err != nil {
			return err
		}
		// -check promises never to write to the tree, and -apply is nothing
		// but writing to it. Honouring either one silently breaks the other.
		if *check && *apply {
			return fmt.Errorf("-check and -apply cannot be combined: -check never writes files")
		}
		if *cpuprofile != "" {
			f, err := os.Create(*cpuprofile)
			if err != nil {
				return err
			}
			defer f.Close()
			if err := pprof.StartCPUProfile(f); err != nil {
				return err
			}
			defer pprof.StopCPUProfile()
		}
		dir := "."
		if fs.NArg() > 0 {
			dir = fs.Arg(0)
		}
		dir, err := filepath.Abs(dir)
		if err != nil {
			return err
		}
		deadcodePath, hasDeadcode := refactor.Tool("deadcode")
		goplsPath, hasGopls := refactor.Tool("gopls")
		egPath, hasEg := refactor.Tool("eg")
		// Templates are looked for next to the code being minimized - the
		// path, its module, and its repository, where /sx bake writes them
		// by default - as well as where sx is run from.
		var defaults []string
		for _, base := range []string{dir, refactor.ModuleRoot(dir), repoRoot(dir), "."} {
			defaults = append(defaults, filepath.Join(base, "examples", "eg"), filepath.Join(base, "sx", "examples", "eg"))
		}
		egTemplates, err := refactor.EgTemplates(egPaths.Values(defaults))
		if err != nil {
			return err
		}
		if !hasDeadcode && !hasGopls && !(hasEg && len(egTemplates) > 0) {
			return fmt.Errorf("install at least one of:\n  go install golang.org/x/tools/cmd/deadcode@latest\n  go install golang.org/x/tools/gopls@latest\n  go install golang.org/x/tools/cmd/eg@latest")
		}
		before, err := scoreTree(dir)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%d nodes\n", before)
		if !hasDeadcode {
			fmt.Fprintln(stdout, "  (deadcode not installed: unreachable functions will not be found)")
		}
		if !hasGopls {
			fmt.Fprintln(stdout, "  (gopls not installed: calls will not be inlined)")
		}
		if len(egTemplates) > 0 && !hasEg {
			fmt.Fprintln(stdout, "  (eg not installed: example rewrites will not be tried)")
		}
		// What a change can break is not what it touches. The gate is the
		// package and everything that imports it, worked out once: these
		// transformations do not add imports, so the set does not move.
		scope := refactor.TestScope(dir)
		if n := len(scope.Packages()); scope.Targets() > 0 {
			fmt.Fprintf(stdout, "  (testing %d packages: the %d under %s and the %d that import them)\n",
				n, scope.Targets(), dir, n-scope.Targets())
		}

		// What already fails before the first change is not the change's
		// doing: it is skipped, and the gate asks only for new failures.
		var baseline refactor.Failures
		if *apply && *runTests {
			start := time.Now()
			baseline, err = scope.Failures(nil)
			if err != nil {
				return err
			}
			if len(baseline) > 0 {
				var names []string
				for k := range baseline {
					pkg, test, _ := strings.Cut(k, "\x00")
					names = append(names, strings.TrimSpace(pkg+" "+test))
				}
				sort.Strings(names)
				fmt.Fprintf(stdout, "  (%d already failing before any change, skipped from now on: %s) %s\n",
					len(names), strings.Join(names, ", "), round(time.Since(start)))
			}
		}

		// An interrupted run stops its tests and puts back a change it had
		// not finished judging, instead of leaving one on disk that nothing
		// decided to keep.
		var pending func() error
		var pendingMu sync.Mutex
		interrupt := make(chan os.Signal, 1)
		signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(interrupt)
		go func() {
			if _, ok := <-interrupt; !ok {
				return
			}
			refactor.StopTests()
			pendingMu.Lock()
			if pending != nil {
				pending()
				fmt.Fprintln(stderr, "sx: interrupted; the change in progress was reverted")
			}
			os.Exit(130)
		}()

		tried := map[string]bool{}
		applied, attempted := 0, 0
		// One cache for the run: a package is re-read only when it, or what it
		// imports, changed since the last pass.
		cache := refactor.NewCache()
		// Where the time goes, per detector, summed over the run.
		spent := map[string]time.Duration{}
		timed := func(name string, f func()) {
			start := time.Now()
			f()
			spent[name] += time.Since(start)
		}
		defer func() {
			var parts []string
			for _, name := range []string{"load", "dead", "inline", "dedup", "eg", "apply+gate"} {
				if d, ok := spent[name]; ok {
					parts = append(parts, fmt.Sprintf("%s %s", name, round(d)))
				}
			}
			fmt.Fprintf(stdout, "time: %s\n", strings.Join(parts, ", "))
		}()
		for attempted < *rounds {
			detect := func() (refactor.Candidate, bool) {
				var all []refactor.Candidate
				timed("load", func() { cache.Begin(dir) })
				add := func(cs []refactor.Candidate, err error) {
					if err == nil {
						all = append(all, cs...)
					}
				}
				if hasDeadcode {
					timed("dead", func() { add(cache.Dead(deadcodePath, dir)) })
				}
				if hasGopls {
					timed("inline", func() { add(cache.Inline(dir)) })
					timed("dedup", func() { add(cache.Duplicates(dir)) })
				}
				if hasEg && len(egTemplates) > 0 {
					timed("eg", func() { add(cache.Eg(dir, egTemplates)) })
				}
				best, found := refactor.Candidate{}, false
				for _, c := range all {
					if tried[c.Key()] || c.Predicted <= 0 {
						continue
					}
					if !found || c.Predicted > best.Predicted {
						best, found = c, true
					}
				}
				return best, found
			}
			c, ok := detect()
			if !ok && hasDeadcode {
				// deadcode's answer is kept between passes; ask it again
				// before concluding there is nothing left.
				cache.ForgetDead()
				c, ok = detect()
			}
			if !ok {
				fmt.Fprintln(stdout, "\nnothing left that the measure says will shrink it")
				break
			}
			attempted++
			tried[c.Key()] = true
			if !*apply {
				where := func() string {
					if r, err := filepath.Rel(dir, c.File); err == nil {
						return r
					}
					return c.File
				}()
				fmt.Fprintf(stdout, "\nwould %s %s at %s:%d, predicted -%d nodes\n    %s\n", c.Kind, c.Target, where, c.Line, c.Predicted, c.Detail)
				// -check stops here rather than applying the candidate to see
				// what it really saves. Proving the saving means writing to
				// the tree, and a check that edits the code it is checking is
				// the wrong shape for CI: the guarantee is worth more than the
				// sharper number.
				if *check {
					return fmt.Errorf("minimization possible: %s %s at %s:%d, predicted -%d nodes", c.Kind, c.Target, where, c.Line, c.Predicted)
				}
				break
			}
			start := time.Now()
			snapshot, err := refactor.Stamp(dir)
			if err != nil {
				return err
			}
			pendingMu.Lock()
			revert, err := refactor.Apply(dir, c, goplsPath, egPath)
			if err == nil {
				pending = revert
			}
			pendingMu.Unlock()
			if err != nil {
				spent["apply+gate"] += time.Since(start)
				fmt.Fprintf(stdout, "  %-2d %7s  skipped  %s %s  %v\n", attempted, round(time.Since(start)), c.Kind, c.Target, err)
				continue
			}
			if err := refactor.Format(dir, snapshot); err != nil {
				// Apply has already written to the tree. Returning here
				// without reverting would leave a change on disk that nothing
				// decided to keep.
				if rerr := revert(); rerr != nil {
					return fmt.Errorf("%w (and the revert failed: %v)", err, rerr)
				}
				return err
			}
			builds, _ := refactor.Repair(dir)
			why := ""
			if !builds {
				if problems, _ := refactor.Build(dir); len(problems) > 0 {
					p := problems[0]
					if r, err := filepath.Rel(dir, p.File); err == nil {
						p.File = r
					}
					why = fmt.Sprintf(": %s:%d: %s", p.File, p.Line, p.Message)
				}
			}
			after, scoreErr := scoreTree(dir)
			var testErr error
			var failures refactor.Failures
			if *runTests && scoreErr == nil && builds {
				if failures, testErr = scope.Failures(baseline); testErr == nil {
					testErr = failures.Since(baseline)
				}
			}
			spent["apply+gate"] += time.Since(start)
			elapsed := round(time.Since(start))
			switch {
			case scoreErr != nil || !builds:
				fmt.Fprintf(stdout, "  %-2d %7s  reverted %s %s  the package stopped building%s\n", attempted, elapsed, c.Kind, c.Target, why)
				if err := revert(); err != nil {
					return err
				}
			case testErr != nil:
				fmt.Fprintf(stdout, "  %-2d %7s  reverted %s %s  the tests failed: %v\n", attempted, elapsed, c.Kind, c.Target, testErr)
				if err := revert(); err != nil {
					return err
				}
				// A failure is the change's only if it goes away without the
				// change. milvus's tracer tests began failing mid-run on their
				// own, and every change after that was blamed for it. So the
				// scope runs again on the reverted tree; what still fails
				// joins the baseline, and if that was all, the candidate is
				// tried again.
				if again, err := scope.Recheck(baseline); err == nil {
					innocent := true
					for k := range failures {
						if !baseline[k] && !again[k] {
							innocent = false
						}
					}
					var joined []string
					for k := range again {
						if !baseline[k] {
							baseline[k] = true
							pkg, test, _ := strings.Cut(k, "\x00")
							joined = append(joined, strings.TrimSpace(pkg+" "+test))
						}
					}
					if len(joined) > 0 {
						sort.Strings(joined)
						fmt.Fprintf(stdout, "      (%s fails without the change too; skipped from now on)\n", strings.Join(joined, ", "))
					}
					if innocent {
						delete(tried, c.Key())
					}
				}
			case after >= before:
				fmt.Fprintf(stdout, "  %-2d %7s  reverted %s %s  %d -> %d nodes, no gain\n", attempted, elapsed, c.Kind, c.Target, before, after)
				if err := revert(); err != nil {
					return err
				}
			default:
				fmt.Fprintf(stdout, "  %-2d %7s  %-6s %-22s %d -> %d (-%d, predicted -%d), tests pass\n", attempted, elapsed, c.Kind, c.Target, before, after, before-after, c.Predicted)
				before = after
				applied++
			}
			pendingMu.Lock()
			pending = nil
			pendingMu.Unlock()
		}
		fmt.Fprintf(stdout, "\n%d nodes after %d changes in %d attempts\n", before, applied, attempted)
		return nil
	}
	fs := flag.NewFlagSet("sx", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", false, "emit JSON")
	limit := fs.Int("n", 20, "how many functions to list (0 for all)")
	tests := fs.Bool("tests", false, "include _test.go files")
	if err := fs.Parse(args); err != nil {
		return err
	}
	targets := fs.Args()
	if len(targets) == 0 {
		targets = []string{"."}
	}

	var report cost.Report
	for _, target := range targets {
		files, err := goFiles(target, *tests)
		if err != nil {
			return err
		}
		for _, f := range files {
			scored, err := cost.ScoreFile(f)
			if err != nil {
				return err
			}
			report.Total += scored.Nodes
			report.Files = append(report.Files, scored)
			report.Functions = append(report.Functions, scored.Functions...)
		}
	}
	cost.Sort(report.Functions)
	if *jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	{
		var limit int = *limit
		fmt.Fprintf(stdout, "%d nodes over %d files, %d functions\n\n", report.Total, len(report.Files), len(report.Functions))
		fmt.Fprintf(stdout, "%7s  %s\n", "NODES", "FUNCTION")
		for i, f := range report.Functions {
			if limit > 0 && i >= limit {
				fmt.Fprintf(stdout, "... %d more\n", len(report.Functions)-i)
				break
			}
			fmt.Fprintf(stdout, "%7d  %s:%d %s\n", f.Nodes, f.File, f.Line, f.Name)
		}
	}
	return nil
}

// goFiles expands a target into the Go files it covers, skipping what the go
// tool itself ignores.
func goFiles(target string, includeTests bool) ([]string, error) {
	info, err := os.Stat(target)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		if !inCurrentBuild(target) {
			return nil, nil
		}
		return []string{target}, nil
	}
	var out []string
	err = filepath.WalkDir(target, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != target && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if !includeTests && strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if !inCurrentBuild(path) {
			return nil
		}
		out = append(out, path)
		return nil
	})
	return out, err
}

func skipDir(name string) bool {
	switch name {
	case "vendor", "testdata":
		return true
	}
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

func inCurrentBuild(path string) bool {
	ok, err := build.Default.MatchFile(filepath.Dir(path), filepath.Base(path))
	return err == nil && ok
}

type pathListFlag struct {
	set    bool
	values []string
}

func (p *pathListFlag) String() string {
	return strings.Join(p.values, string(os.PathListSeparator))
}

func (p *pathListFlag) Set(value string) error {
	p.set = true
	for _, part := range strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == rune(os.PathListSeparator)
	}) {
		part = strings.TrimSpace(part)
		if part != "" {
			p.values = append(p.values, part)
		}
	}
	return nil
}

func (p *pathListFlag) Values(defaults []string) []string {
	if p.set {
		return append([]string(nil), p.values...)
	}
	return append([]string(nil), defaults...)
}

// repoRoot is the top of the git repository dir is in, or dir itself.
func repoRoot(dir string) string {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	if root := strings.TrimSpace(string(out)); err == nil && root != "" {
		return root
	}
	return dir
}
