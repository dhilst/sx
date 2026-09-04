package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"purgatrix/internal/cost"
	"purgatrix/internal/refactor"
)

// cmdRefactor lowers the measured cost by extracting nested blocks.
//
// The division of labour is the point: gopls performs the extraction, because
// it has the type information needed to work out free variables and return
// values and to refuse when the extraction is not legal. Choosing which block
// to extract is ours, because gopls has no notion of which one is worth it.
//
// Nothing is kept on trust. After each extraction the tree is re-scored, and a
// change that does not lower the total, or that stops the package building, is
// reverted.
func cmdRefactor(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("refactor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	apply := fs.Bool("apply", false, "write changes; without it, only report what would be tried")
	maxEdits := fs.Int("n", 5, "how many extractions to attempt")
	minStmts := fs.Int("min-statements", 3, "smallest block worth extracting")
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

	goplsPath, ok := refactor.Available()
	if !ok {
		return fmt.Errorf("gopls is not installed: go install golang.org/x/tools/gopls@latest")
	}
	weights := cost.DefaultWeights()

	before, err := scoreTree(dir, weights)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "cost %d\n", before)

	applied, attempted := 0, 0
	for applied < *maxEdits {
		candidates, err := treeCandidates(dir, weights, *minStmts)
		if err != nil {
			return err
		}
		if len(candidates) == 0 {
			fmt.Fprintln(stdout, "\nno block left that the model expects to pay")
			break
		}
		c := candidates[0]
		attempted++
		if !*apply {
			fmt.Fprintf(stdout, "\nwould extract %s:%d-%d from %s (%d statements at depth %d, predicted -%d)\n",
				rel(dir, c.File), c.StartLine, c.EndLine, c.Function, c.Statements, c.Depth, c.Predicted)
			break
		}

		original, err := os.ReadFile(c.File)
		if err != nil {
			return err
		}
		revert := func() error { return os.WriteFile(c.File, original, 0o644) }

		if err := refactor.Extract(goplsPath, dir, c); err != nil {
			fmt.Fprintf(stdout, "  skipped %s:%d  %v\n", rel(dir, c.File), c.StartLine, err)
			if err := revert(); err != nil {
				return err
			}
			// Nothing changed, so the same candidate would be chosen again.
			if attempted > *maxEdits*3 {
				break
			}
			if err := blockCandidate(dir, c); err != nil {
				return err
			}
			continue
		}

		after, scoreErr := scoreTree(dir, weights)
		buildErr := buildOK(dir)
		switch {
		case scoreErr != nil || buildErr != nil:
			reason := "the package stopped building"
			if scoreErr != nil {
				reason = "the result would not parse"
			}
			fmt.Fprintf(stdout, "  reverted %s:%d  %s\n", rel(dir, c.File), c.StartLine, reason)
			if err := revert(); err != nil {
				return err
			}
		case after >= before:
			fmt.Fprintf(stdout, "  reverted %s:%d  cost %d -> %d, no gain\n",
				rel(dir, c.File), c.StartLine, before, after)
			if err := revert(); err != nil {
				return err
			}
			if err := blockCandidate(dir, c); err != nil {
				return err
			}
		default:
			fmt.Fprintf(stdout, "  extracted %s:%d from %s  cost %d -> %d (-%d, predicted -%d)\n",
				rel(dir, c.File), c.StartLine, c.Function, before, after, before-after, c.Predicted)
			before = after
			applied++
		}
	}

	fmt.Fprintf(stdout, "\ncost %d after %d extractions\n", before, applied)
	if *apply && applied > 0 {
		fmt.Fprintln(stdout, "the cost is measured and the package builds; behaviour is not verified. run the tests.")
	}
	return nil
}

// blocked remembers candidates gopls refused or that did not pay, so the loop
// moves on instead of choosing the same block forever.
var blocked = map[string]bool{}

func blockCandidate(dir string, c refactor.Candidate) error {
	blocked[fmt.Sprintf("%s:%d:%d", c.File, c.StartLine, c.StartCol)] = true
	return nil
}

func treeCandidates(dir string, w cost.Weights, minStmts int) ([]refactor.Candidate, error) {
	files, err := goFiles(dir, false)
	if err != nil {
		return nil, err
	}
	var out []refactor.Candidate
	for _, f := range files {
		cs, err := refactor.Candidates(f, w, minStmts)
		if err != nil {
			continue // a file that will not parse is not a candidate source
		}
		for _, c := range cs {
			if c.Predicted <= 0 {
				continue
			}
			if blocked[fmt.Sprintf("%s:%d:%d", c.File, c.StartLine, c.StartCol)] {
				continue
			}
			out = append(out, c)
		}
	}
	sortCandidates(out)
	return out, nil
}

func sortCandidates(cs []refactor.Candidate) {
	for i := 1; i < len(cs); i++ {
		for j := i; j > 0 && cs[j].Predicted > cs[j-1].Predicted; j-- {
			cs[j], cs[j-1] = cs[j-1], cs[j]
		}
	}
}

func scoreTree(dir string, w cost.Weights) (int, error) {
	files, err := goFiles(dir, false)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, f := range files {
		fns, err := cost.ScoreFile(f, w)
		if err != nil {
			return 0, err
		}
		for _, fn := range fns {
			total += fn.Total
		}
	}
	return total, nil
}

func buildOK(dir string) error {
	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = dir
	return cmd.Run()
}

func rel(dir, path string) string {
	if r, err := filepath.Rel(dir, path); err == nil {
		return r
	}
	return path
}
