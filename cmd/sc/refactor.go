package main

import (
	"flag"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"time"

	"purgatrix/internal/cost"
	"purgatrix/internal/refactor"
)

// cmdRefactor makes the program smaller without changing what it does.
//
// Detection is this tool's job; the transformations belong to whoever does them
// properly. deadcode answers reachability across the whole program, and gopls
// inlines a call with the type information needed to know that substituting
// arguments changes neither the effects nor their order.
//
// Nothing is kept on trust. After each change the tree is re-counted, rebuilt
// and re-tested, and anything that fails one of those, or does not make the
// program smaller, is put back.
func cmdRefactor(args []string, stdout, stderr io.Writer) error {
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

	tried := map[string]bool{}
	applied, attempted := 0, 0
	for attempted < *rounds {
		c, ok := next(dir, deadcodePath, hasDeadcode, hasGopls, tried)
		if !ok {
			fmt.Fprintln(stdout, "\nnothing left that the measure says will shrink it")
			break
		}
		attempted++
		tried[c.Key()] = true
		if !*apply {
			fmt.Fprintf(stdout, "\nwould %s %s at %s:%d, predicted -%d nodes\n    %s\n",
				c.Kind, c.Target, rel(dir, c.File), c.Line, c.Predicted, c.Detail)
			break
		}

		start := time.Now()
		revert, err := refactor.Apply(dir, c, goplsPath)
		if err != nil {
			fmt.Fprintf(stdout, "  %-2d %7s  skipped  %s %s  %v\n",
				attempted, round(time.Since(start)), c.Kind, c.Target, err)
			continue
		}

		// A change that leaves the tree not building may simply be
		// unfinished. Repair what can be repaired before judging it.
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
			testErr = testsPass(dir)
		}
		elapsed := round(time.Since(start))

		switch {
		case scoreErr != nil || !builds:
			fmt.Fprintf(stdout, "  %-2d %7s  reverted %s %s  the package stopped building%s\n",
				attempted, elapsed, c.Kind, c.Target, repaired)
			if err := revert(); err != nil {
				return err
			}
		case testErr != nil:
			fmt.Fprintf(stdout, "  %-2d %7s  reverted %s %s  the tests failed\n",
				attempted, elapsed, c.Kind, c.Target)
			if err := revert(); err != nil {
				return err
			}
		case after >= before:
			fmt.Fprintf(stdout, "  %-2d %7s  reverted %s %s  %d -> %d nodes, no gain\n",
				attempted, elapsed, c.Kind, c.Target, before, after)
			if err := revert(); err != nil {
				return err
			}
		default:
			fmt.Fprintf(stdout, "  %-2d %7s  %-6s %-22s %d -> %d (-%d, predicted -%d), tests pass\n",
				attempted, elapsed, c.Kind, c.Target, before, after, before-after, c.Predicted)
			before = after
			applied++
		}
	}

	fmt.Fprintf(stdout, "\n%d nodes after %d changes in %d attempts\n", before, applied, attempted)
	return nil
}

// next picks the largest saving on offer that has not already been tried.
// Detection is redone each round because every applied change moves the code.
func next(dir, deadcodePath string, hasDeadcode, hasGopls bool, tried map[string]bool) (refactor.Candidate, bool) {
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
}

func scoreTree(dir string) (int, error) {
	files, err := goFiles(dir, false)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, f := range files {
		scored, err := cost.ScoreFile(f)
		if err != nil {
			return 0, err
		}
		total += scored.Nodes
	}
	return total, nil
}

func testsPass(dir string) error {
	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = dir
	return cmd.Run()
}

func round(d time.Duration) string { return d.Round(time.Millisecond).String() }

func rel(dir, path string) string {
	if r, err := filepath.Rel(dir, path); err == nil {
		return r
	}
	return path
}
