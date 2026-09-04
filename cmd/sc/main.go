// Command sc measures a Go program as |AST| - the number of nodes it takes to
// express - and shrinks it without changing what it does.
//
// The measure has no parameters, ignores formatting and comments, and is
// additive: a node costs one wherever it sits. "sc refactor" removes dead
// code, inlines abstractions used once, and factors out duplication, keeping
// only the changes that survive a build, the tests of every package that could
// be affected, and a re-count.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"purgatrix/internal/refactor"
	"strings"
	"time"

	"purgatrix/internal/cost"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "sc:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) > 0 && args[0] == "refactor" {
		var args []string = args[1:]
		fs := flag.NewFlagSet("refactor", flag.ContinueOnError)
		fs.SetOutput(stderr)
		apply := fs.Bool("apply", false, "write changes; without it, list what would be tried")
		rounds := fs.Int("n", 10, "how many changes to attempt")
		runTests := fs.Bool("test", true, "run the tests after each change and revert if they fail")
		if err := fs.Parse(args); err != nil {
			return err
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
		if !hasDeadcode && !hasGopls {
			return fmt.Errorf("install at least one of:\n  go install golang.org/x/tools/cmd/deadcode@latest\n  go install golang.org/x/tools/gopls@latest")
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
		// What a change can break is not what it touches. The gate is the
		// package and everything that imports it, worked out once: these
		// transformations do not add imports, so the set does not move.
		scope := refactor.TestScope(dir)
		if n := len(scope.Packages()); n > 1 {
			fmt.Fprintf(stdout, "  (testing %d packages: %s and the %d that import it)\n",
				n, scope.Packages()[0], n-1)
		}

		tried := map[string]bool{}
		applied, attempted := 0, 0
		for attempted < *rounds {
			c, ok := func() (refactor.Candidate, bool) {
				var dir string = dir
				var all []refactor.Candidate
				if hasDeadcode {
					if cs, err := refactor.DeadCandidates(deadcodePath, dir); err == nil {
						all = append(all, cs...)
					}
				}
				if hasGopls {
					if cs, err := refactor.InlineCandidates(dir); err == nil {
						all = append(all, cs...)
					}
					if cs, err := refactor.DuplicateCandidates(dir); err == nil {
						all = append(all, cs...)
					}
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
			}()
			if !ok {
				fmt.Fprintln(stdout, "\nnothing left that the measure says will shrink it")
				break
			}
			attempted++
			tried[c.Key()] = true
			if !*apply {
				fmt.Fprintf(stdout, "\nwould %s %s at %s:%d, predicted -%d nodes\n    %s\n", c.Kind, c.Target, func() string {
					if r, err := filepath.Rel(dir, c.File); err == nil {
						return r
					}
					return c.File
				}(), c.Line, c.Predicted, c.Detail)
				break
			}
			start := time.Now()
			revert, err := refactor.Apply(dir, c, goplsPath)
			if err != nil {
				fmt.Fprintf(stdout, "  %-2d %7s  skipped  %s %s  %v\n", attempted, round(time.Since(start)), c.Kind, c.Target, err)
				continue
			}
			if err := refactor.Format(dir, start); err != nil {
				return err
			}
			builds, repairErr := refactor.Repair(dir)
			repaired := ""
			if builds && repairErr == nil {
				if problems, _ := refactor.Build(dir); len(problems) == 0 {
					repaired = ""
				}
			}
			after, scoreErr := scoreTree(dir)
			var testErr error
			if *runTests && scoreErr == nil && builds {
				testErr = scope.Test()
			}
			elapsed := round(time.Since(start))
			switch {
			case scoreErr != nil || !builds:
				fmt.Fprintf(stdout, "  %-2d %7s  reverted %s %s  the package stopped building%s\n", attempted, elapsed, c.Kind, c.Target, repaired)
				if err := revert(); err != nil {
					return err
				}
			case testErr != nil:
				fmt.Fprintf(stdout, "  %-2d %7s  reverted %s %s  the tests failed\n", attempted, elapsed, c.Kind, c.Target)
				if err := revert(); err != nil {
					return err
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
		}
		fmt.Fprintf(stdout, "\n%d nodes after %d changes in %d attempts\n", before, applied, attempted)
		return nil
	}
	fs := flag.NewFlagSet("sc", flag.ContinueOnError)
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
