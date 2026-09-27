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
		fs, egPaths, apply, check, rounds, runTests, useLSP, batch, cpuprofile := newFunction4(stderr)
		fs.Var(&egPaths, "eg", "file or directory of eg templates; repeat or separate with commas/path-list separators (empty disables eg)")
		err, shouldReturn := newFunction17(fs, args, check, apply)
		if shouldReturn {
			return err
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
		dir, deadcodePath, hasDeadcode, goplsPath, hasGopls, egPath, hasEg, defaults, err := newFunction6(fs)
		if err != nil {
			return err
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
		scope := newFunction5(stdout, before, hasDeadcode, hasGopls, egTemplates, hasEg, dir)

		if *apply && hasGopls && *useLSP {
			l, err := refactor.StartLSP(goplsPath, refactor.ModuleRoot(dir))
			if err != nil {
				fmt.Fprintf(stdout, "  (gopls session did not start, using the command line: %v)\n", err)
			} else {
				refactor.UseLSP(l)
				defer func() { refactor.UseLSP(nil); l.Close() }()
			}
		}

		var hist *refactor.History
		if *batch {
			if !*apply || !*runTests {
				return fmt.Errorf("-batch needs -apply and the tests")
			}
			if hist, err = refactor.OpenHistory(dir); err != nil {
				return err
			}
			scope = scope.Record(hist.Recorder())
			fmt.Fprintf(stdout, "  (batched: each change is committed and the tests run once at the end; run %s)\n", hist.Run)
		}

		// What already fails before the first change is not the change's
		// doing: it is skipped, and the gate asks only for new failures.
		// Tests recorded as flaky by earlier runs are skipped from the start.
		var baseline refactor.Failures
		testTime, testRuns := time.Duration(0), 0
		if *apply && *runTests {
			start := time.Now()
			known := refactor.FlakyTests(dir)
			baseline, err = scope.Failures(known)
			if err != nil {
				return err
			}
			testTime, testRuns = newFunction3(known, baseline, testTime, testRuns, start, stdout)
		}

		// An interrupted run stops its tests and puts back a change it had
		// not finished judging, instead of leaving one on disk that nothing
		// decided to keep.
		var pendingMu sync.Mutex
		pending, interrupt := newFunction14()
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

		tried, unstable, applied, attempted, cache, spent, timed := newFunction9()
		defer func() {
			var parts []string
			for _, name := range []string{"load", "dead", "inline", "dedup", "extract", "heuristic", "eg", "apply+gate"} {
				if d, ok := spent[name]; ok {
					parts = append(parts, fmt.Sprintf("%s %s", name, round(d)))
				}
			}
			fmt.Fprintf(stdout, "time: %s\n", strings.Join(parts, ", "))
		}()
		dropped := 0
	batched:
		for attempted < *rounds {
			c, ok := newFunction7(timed, cache, dir, hasDeadcode, deadcodePath, hasGopls, hasEg, egTemplates, tried)
			if !ok {
				fmt.Fprintln(stdout, "\nnothing left that the measure says will lower it")
				break
			}
			attempted++
			tried[c.Key()] = true
			if !*apply {
				where := newFunction8(dir, c, stdout)
				// -check stops here rather than applying the candidate to see
				// what it really saves. Proving the saving means writing to
				// the tree, and a check that edits the code it is checking is
				// the wrong shape for CI: the guarantee is worth more than the
				// sharper number.
				if *check {
					return fmt.Errorf("minimization possible: %s %s at %s:%d, predicted %+d nodes, J -%.1f", c.Kind, c.Target, where, c.Line, -c.Predicted, c.Gain)
				}
				break
			}
			start, snapshot, err1 := newFunction16(dir)
			if err1 != nil {
				return err1
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
			builds, why, after, scoreErr, testErr, failures, elapsed, err1, shouldReturn := newFunction2(dir, snapshot, revert, runTests, batch, scope, baseline, spent, start)
			if shouldReturn {
				return err1
			}
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
				scope = newFunction(scope, baseline, failures, unstable, stdout, tried, c)
			case after.Objective >= before.Objective:
				fmt.Fprintf(stdout, "  %-2d %7s  reverted %s %s  %s -> %s, no gain\n", attempted, elapsed, c.Kind, c.Target, before, after)
				if err := revert(); err != nil {
					return err
				}
			default:
				err2 := newFunction11(hist, c, before, after, stdout, attempted, elapsed)
				if err2 != nil {
					return err2
				}
				before = after
				applied++
			}
			pendingMu.Lock()
			pending = nil
			pendingMu.Unlock()
		}
		if hist != nil {
			// Test the batch; if a change broke something, go back to just
			// before it and carry on detecting from there.
			bad, why, err := verifyBatch(stdout, hist, scope, baseline, &testTime, &testRuns)
			if err != nil {
				return err
			}
			if bad != "" {
				kind, err1 := newFunction10(hist, bad, tried, why)
				if err1 != nil {
					return err1
				}
				dropped++
				before, _ = scoreTree(dir)
				applied = len(hist.Commits())
				fmt.Fprintf(stdout, "      dropped the %s at %s (%v) and the changes after it; detecting again\n", kind, short(bad), why)
				goto batched
			}
			kept, err := finishBatch(stdout, dir, hist, attempted, dropped, spent, testTime, testRuns)
			if err != nil {
				return err
			}
			before, _ = scoreTree(dir)
			applied = kept
		}
		fmt.Fprintf(stdout, "\n%s after %d changes in %d attempts\n", before, applied, attempted)
		return nil
	}
	return newFunction1(args, stdout, stderr)
}

func newFunction17(fs *flag.FlagSet, args []string, check *bool, apply *bool) (error, bool) {
	if err := fs.Parse(args); err != nil {
		return err, true
	}
	// -check promises never to write to the tree, and -apply is nothing
	// but writing to it. Honouring either one silently breaks the other.
	if *check && *apply {
		return fmt.Errorf("-check and -apply cannot be combined: -check never writes files"), true
	}
	return nil, false
}

func newFunction16(dir string) (time.Time, refactor.Snapshot, error) {
	start := time.Now()
	snapshot, err := refactor.Stamp(dir)
	if err != nil {
		return time.Time{}, nil, err
	}
	return start, snapshot, nil
}

func newFunction14() (func() error, chan os.Signal) {
	var pending func() error
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)
	return pending, interrupt
}

func newFunction11(hist *refactor.History, c refactor.Candidate, before cost.Tree, after cost.Tree, stdout io.Writer, attempted int, elapsed string) error {
	verdict := "tests pass"
	if hist != nil {
		verdict = "committed"
		if err := hist.Commit(c, before, after); err != nil {
			return err
		}
	}
	fmt.Fprintf(stdout, "  %-2d %7s  %-7s %-22s %d -> %d nodes (%+d, predicted %+d), J %.1f -> %.1f (%+.1f, predicted %+.1f), %s\n",
		attempted, elapsed, c.Kind, c.Target, before.Nodes, after.Nodes, after.Nodes-before.Nodes, -c.Predicted,
		before.Objective, after.Objective, after.Objective-before.Objective, -c.Gain, verdict)
	return nil
}

func newFunction10(hist *refactor.History, bad string, tried map[string]bool, why error) (refactor.Kind, error) {
	key, kind := hist.KeyOf(bad), hist.KindOf(bad)
	// The changes after the bad one go with it, but only the bad
	// one was at fault: the others may be offered again.
	commits := hist.Commits()
	for i := len(commits) - 1; i >= 0 && commits[i] != bad; i-- {
		delete(tried, hist.KeyOf(commits[i]))
	}
	if err := hist.ResetTo(bad+"^", why.Error()); err != nil {
		return "", err
	}
	tried[key] = true
	return kind, nil
}

func newFunction9() (map[string]bool, map[string]int, int, int, *refactor.Cache, map[string]time.Duration, func(name string, f func())) {
	tried := map[string]bool{}
	unstable := map[string]int{} // package -> rechecks it failed on its own
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
	return tried, unstable, applied, attempted, cache, spent, timed
}

func newFunction8(dir string, c refactor.Candidate, stdout io.Writer) string {
	where := func() string {
		if r, err := filepath.Rel(dir, c.File); err == nil {
			return r
		}
		return c.File
	}()
	fmt.Fprintf(stdout, "\nwould %s %s at %s:%d, predicted %+d nodes, J -%.1f\n    %s\n", c.Kind, c.Target, where, c.Line, -c.Predicted, c.Gain, c.Detail)
	return where
}

func newFunction7(timed func(name string, f func()), cache *refactor.Cache, dir string, hasDeadcode bool, deadcodePath string, hasGopls bool, hasEg bool, egTemplates []string, tried map[string]bool) (refactor.Candidate, bool) {
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
			timed("extract", func() { add(cache.Extractions(dir)) })
			timed("heuristic", func() { add(cache.Heuristics(dir)) })
		}
		if hasEg && len(egTemplates) > 0 {
			timed("eg", func() { add(cache.Eg(dir, egTemplates)) })
		}
		best, found := refactor.Candidate{}, false
		for _, c := range all {
			if tried[c.Key()] || c.Gain <= 0 {
				continue
			}
			if !found || c.Gain > best.Gain {
				best, found = c, true
			}
		}
		return best, found
	}
	return newFunction21(detect, hasDeadcode, cache)
}

func newFunction21(detect func() (refactor.Candidate, bool), hasDeadcode bool, cache *refactor.Cache) (refactor.Candidate, bool) {
	c, ok := detect()
	if !ok && hasDeadcode {
		// deadcode's answer is kept between passes; ask it again
		// before concluding there is nothing left.
		cache.ForgetDead()
		c, ok = detect()
	}
	return c, ok
}

func newFunction6(fs *flag.FlagSet) (string, string, bool, string, bool, string, bool, []string, error) {
	dir := "."
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", "", false, "", false, "", false, nil, err
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
	return dir, deadcodePath, hasDeadcode, goplsPath, hasGopls, egPath, hasEg, defaults, nil
}

func newFunction5(stdout io.Writer, before cost.Tree, hasDeadcode bool, hasGopls bool, egTemplates []string, hasEg bool, dir string) refactor.Scope {
	fmt.Fprintf(stdout, "%s (mB=%g, H=%g)\n", before, cost.Target(), cost.H)
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
	return scope
}

func newFunction4(stderr io.Writer) (*flag.FlagSet, pathListFlag, *bool, *bool, *int, *bool, *bool, *bool, *string) {
	fs := flag.NewFlagSet("refactor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var egPaths pathListFlag
	apply := fs.Bool("apply", false, "write changes; without it, list what would be tried")
	check := fs.Bool("check", false, "exit non-zero at the first shrinking candidate; never writes to the tree")
	rounds := fs.Int("n", 10, "how many changes to attempt")
	runTests := fs.Bool("test", true, "run the tests after each change and revert if they fail")
	useLSP := fs.Bool("lsp", true, "keep one gopls session over stdio for the run instead of launching gopls for each change")
	batch := fs.Bool("batch", false, "commit each change and test once at the end, bisecting any failure to the change that caused it; needs -apply and a clean git tree")
	cpuprofile := fs.String("cpuprofile", "", "write a CPU profile of the run to this file")
	newFunction22(fs)
	return fs, egPaths, apply, check, rounds, runTests, useLSP, batch, cpuprofile
}

func newFunction22(fs *flag.FlagSet) {
	fs.Float64Var(&cost.Block, "block", cost.Block, "B, the weight (statements × depth) a function should have on average")
	fs.Float64Var(&cost.H, "overhead", cost.H, "H, what extracting a function typically costs in nodes")
	fs.Float64Var(&cost.M, "m", cost.M, "the multiplier: aim at functions weighing m·B; any positive number")
}

func newFunction3(known refactor.Failures, baseline refactor.Failures, testTime time.Duration, testRuns int, start time.Time, stdout io.Writer) (time.Duration, int) {
	for k := range known {
		baseline[k] = true
	}
	testTime, testRuns = time.Since(start), 1
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
	return testTime, testRuns
}

func newFunction2(dir string, snapshot refactor.Snapshot, revert func() error, runTests *bool, batch *bool, scope refactor.Scope, baseline refactor.Failures, spent map[string]time.Duration, start time.Time) (bool, string, cost.Tree, error, error, refactor.Failures, string, error, bool) {
	if err := refactor.Format(dir, snapshot); err != nil {
		// Apply has already written to the tree. Returning here
		// without reverting would leave a change on disk that nothing
		// decided to keep.
		if rerr := revert(); rerr != nil {
			return false, "", cost.Tree{}, nil, nil, nil, "", fmt.Errorf("%w (and the revert failed: %v)", err, rerr), true
		}
		return false, "", cost.Tree{}, nil, nil, nil, "", err, true
	}
	builds, _ := refactor.Repair(dir)
	why := ""
	if !builds {
		why = newFunction23(dir, why)
	}
	after, scoreErr := scoreTree(dir)
	var testErr error
	var failures refactor.Failures
	if *runTests && !*batch && scoreErr == nil && builds {
		if failures, testErr = scope.Failures(baseline); testErr == nil {
			testErr = failures.Since(baseline)
		}
	}
	spent["apply+gate"] += time.Since(start)
	elapsed := round(time.Since(start))
	return builds, why, after, scoreErr, testErr, failures, elapsed, nil, false
}

func newFunction23(dir string, why string) string {
	if problems, _ := refactor.Build(dir); len(problems) > 0 {
		p := problems[0]
		if r, err := filepath.Rel(dir, p.File); err == nil {
			p.File = r
		}
		why = fmt.Sprintf(": %s:%d: %s", p.File, p.Line, p.Message)
	}
	return why
}

func newFunction1(args []string, stdout io.Writer, stderr io.Writer) error {
	if len(args) > 0 && args[0] == "status" {
		dir := "."
		if len(args) > 1 {
			dir = args[1]
		}
		return status(stdout, dir)
	}
	fs := flag.NewFlagSet("sx", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", false, "emit JSON")
	limit := fs.Int("n", 20, "how many functions to list (0 for all)")
	tests := fs.Bool("tests", false, "include _test.go files")
	byWeight := fs.Bool("weight", false, "list the heaviest functions rather than the largest")
	newFunction22(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	targets := fs.Args()
	if len(targets) == 0 {
		targets = []string{"."}
	}

	return newFunction19(targets, tests, byWeight, jsonOut, stdout, limit)
}

func newFunction19(targets []string, tests *bool, byWeight *bool, jsonOut *bool, stdout io.Writer, limit *int) error {
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
	var weights []int
	for _, f := range report.Functions {
		weights = append(weights, f.Weight)
	}
	report.Objective = cost.Objective(report.Total, weights)
	return newFunction12(report, byWeight, jsonOut, stdout, limit, weights)
}

func newFunction12(report cost.Report, byWeight *bool, jsonOut *bool, stdout io.Writer, limit *int, weights []int) error {
	cost.Sort(report.Functions)
	if *byWeight {
		sort.SliceStable(report.Functions, func(i, j int) bool { return report.Functions[i].Weight > report.Functions[j].Weight })
	}
	if *jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	{
		var limit int = *limit
		newFunction18(stdout, report, weights, limit)
	}
	return nil
}

func newFunction18(stdout io.Writer, report cost.Report, weights []int, limit int) {
	fmt.Fprintf(stdout, "%d nodes over %d files, %d functions\n", report.Total, len(report.Files), len(report.Functions))
	mean := 0.0
	if len(weights) > 0 {
		total := 0
		for _, w := range weights {
			total += w
		}
		mean = float64(total) / float64(len(weights))
	}
	fmt.Fprintf(stdout, "J %.1f, mean weight %.1f (mB=%g, H=%g)\n\n", report.Objective, mean, cost.Target(), cost.H)
	fmt.Fprintf(stdout, "%7s %7s %8s  %s\n", "NODES", "WEIGHT", "LOAD", "FUNCTION")
	for i, f := range report.Functions {
		if limit > 0 && i >= limit {
			fmt.Fprintf(stdout, "... %d more\n", len(report.Functions)-i)
			break
		}
		fmt.Fprintf(stdout, "%7d %7d %8.1f  %s:%d %s\n", f.Nodes, f.Weight, cost.Load(f.Weight), f.File, f.Line, f.Name)
	}
}

func newFunction(scope refactor.Scope, baseline refactor.Failures, failures refactor.Failures, unstable map[string]int, stdout io.Writer, tried map[string]bool, c refactor.Candidate) refactor.Scope {
	if again, err := scope.Recheck(baseline); err == nil {
		innocent := true
		for k := range failures {
			if !baseline[k] && !again[k] {
				innocent = false
			}
		}
		scope = newFunction15(again, baseline, unstable, scope, stdout)
		if innocent {
			delete(tried, c.Key())
		}
	}
	return scope
}

func newFunction15(again refactor.Failures, baseline refactor.Failures, unstable map[string]int, scope refactor.Scope, stdout io.Writer) refactor.Scope {
	joined, flaked := newFunction25(again, baseline)
	// A package that fails on its own at two separate
	// rechecks is not flaky in one test but in its order or
	// shared state: milvus's paramtable failed a different
	// test each run. Skipping tests one by one never ends
	// there, so the package leaves the gate.
	for pkg := range flaked {
		if pkg == "" {
			continue
		}
		unstable[pkg]++
		if unstable[pkg] == 2 {
			scope = scope.Without(pkg)
			fmt.Fprintf(stdout, "      (%s fails on its own at every recheck; no longer gating)\n", pkg)
		}
	}
	if len(joined) > 0 {
		sort.Strings(joined)
		fmt.Fprintf(stdout, "      (%s fails without the change too; skipped from now on)\n", strings.Join(joined, ", "))
	}
	return scope
}

func newFunction25(again refactor.Failures, baseline refactor.Failures) ([]string, map[string]bool) {
	var joined []string
	flaked := map[string]bool{}
	for k := range again {
		if !baseline[k] {
			baseline[k] = true
			pkg, test, _ := strings.Cut(k, "\x00")
			joined = append(joined, strings.TrimSpace(pkg+" "+test))
			flaked[pkg] = true
		}
	}
	return joined, flaked
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
	return newFunction20(err, target, includeTests)
}

func newFunction20(err error, target string, includeTests bool) ([]string, error) {
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
