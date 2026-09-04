package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"purgatrix/internal/refactor"
)

// cmdBeam searches sequences of changes rather than taking the best one at
// each step.
//
// Greedy is wrong whenever a move only pays by enabling another. Inlining a
// call is the case that made this concrete: on its own it always makes the
// program bigger, because the body then exists both at the call site and in the
// declaration, and only becomes a saving once the declaration goes. That
// particular pair is now applied as one move, but the general shape recurs -
// removing one function can strand another, which strands a third - and a
// greedy step cannot see past the first.
//
// This is the phase-ordering problem, and a beam is the cheap answer to it:
// keep the best few states at each depth, extend each of them, and let the
// arithmetic decide which survive.
func cmdBeam(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("beam", flag.ContinueOnError)
	fs.SetOutput(stderr)
	width := fs.Int("width", 3, "states carried to the next depth")
	depth := fs.Int("depth", 3, "how many changes deep to search")
	branch := fs.Int("branch", 3, "candidates tried per state")
	patience := fs.Int("patience", 2, "depths without improvement before giving up")
	apply := fs.Bool("apply", false, "write the winning sequence back to the source")
	runTests := fs.Bool("test", true, "require the tests to pass at every step")
	if err := fs.Parse(args); err != nil {
		return err
	}
	target := "."
	if fs.NArg() > 0 {
		target = fs.Arg(0)
	}
	target, err := filepath.Abs(target)
	if err != nil {
		return err
	}

	deadcodePath, hasDeadcode := refactor.Tool("deadcode")
	goplsPath, hasGopls := refactor.Tool("gopls")
	if !hasDeadcode && !hasGopls {
		return fmt.Errorf("install deadcode or gopls first")
	}

	work, err := os.MkdirTemp("", "sc-beam-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)

	root, err := newState(work, "root", target)
	if err != nil {
		return err
	}
	root.nodes, err = scoreTree(root.dir)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%d nodes\n\n", root.nodes)
	fmt.Fprintf(stdout, "%-6s %-5s %7s  %s\n", "DEPTH", "STATE", "NODES", "SEQUENCE")

	best := root
	frontier := []*state{root}
	seen := map[string]bool{root.fingerprint: true}
	stale, evaluated := 0, 0
	start := time.Now()

	for d := 1; d <= *depth; d++ {
		var next []*state
		for _, s := range frontier {
			for _, c := range topCandidates(s.dir, deadcodePath, hasDeadcode, hasGopls, *branch, s.tried) {
				child, err := newState(work, fmt.Sprintf("d%d-%d", d, evaluated), s.dir)
				if err != nil {
					return err
				}
				evaluated++
				child.moves = append(append([]string(nil), s.moves...), fmt.Sprintf("%s %s", c.Kind, c.Target))
				child.tried = copyTried(s.tried)
				child.tried[c.Key()] = true

				moved, err := rebase(c, s.dir, child.dir)
				if err != nil {
					os.RemoveAll(child.dir)
					continue
				}
				if !child.attempt(moved, goplsPath, *runTests) {
					os.RemoveAll(child.dir)
					continue
				}
				if seen[child.fingerprint] {
					os.RemoveAll(child.dir) // the same program by another route
					continue
				}
				seen[child.fingerprint] = true
				next = append(next, child)
			}
		}
		if len(next) == 0 {
			fmt.Fprintf(stdout, "%-6d %s\n", d, "no change left that keeps the program working")
			break
		}
		sort.SliceStable(next, func(i, j int) bool { return next[i].nodes < next[j].nodes })
		if len(next) > *width {
			for _, dead := range next[*width:] {
				os.RemoveAll(dead.dir)
			}
			next = next[:*width]
		}
		for i, s := range next {
			fmt.Fprintf(stdout, "%-6d %-5d %7d  %s\n", d, i+1, s.nodes, strings.Join(s.moves, " -> "))
		}
		if next[0].nodes < best.nodes {
			best = next[0]
			stale = 0
		} else {
			stale++
			// A depth that buys nothing is allowed, because the move that pays
			// may be the one after it. Several in a row is a dead end.
			if stale >= *patience {
				fmt.Fprintf(stdout, "\n%d depths without improvement; stopping\n", stale)
				break
			}
		}
		frontier = next
	}

	fmt.Fprintf(stdout, "\nbest: %d nodes (-%d, %.1f%%) after %d changes, %d states evaluated in %s\n",
		best.nodes, root.nodes-best.nodes,
		100*float64(root.nodes-best.nodes)/float64(max(1, root.nodes)),
		len(best.moves), evaluated, time.Since(start).Round(time.Second))
	for i, m := range best.moves {
		fmt.Fprintf(stdout, "  %d. %s\n", i+1, m)
	}
	if !*apply {
		if len(best.moves) > 0 {
			fmt.Fprintln(stdout, "\nnot written; pass -apply to keep it")
		}
		return nil
	}
	if len(best.moves) == 0 {
		return nil
	}
	if err := copyTree(best.dir, target); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "\nwritten to %s\n", target)
	return nil
}

// state is one program the search is holding on to, kept as a directory of its
// own so that paths cannot disturb each other.
type state struct {
	dir         string
	nodes       int
	moves       []string
	tried       map[string]bool
	fingerprint string
}

func newState(work, name, from string) (*state, error) {
	dir := filepath.Join(work, name)
	if err := copyTree(from, dir); err != nil {
		return nil, err
	}
	return &state{dir: dir, tried: map[string]bool{}}, nil
}

// rebase points a candidate at a copy of the tree it was found in. Candidates
// are detected in the parent state's directory; applying one without moving its
// path would edit the parent and leave the child untouched.
func rebase(c refactor.Candidate, from, to string) (refactor.Candidate, error) {
	rel, err := filepath.Rel(from, c.File)
	if err != nil {
		return c, err
	}
	c.File = filepath.Join(to, rel)
	return c, nil
}

// attempt applies a change and reports whether the result is worth keeping: it
// has to build, pass the tests, and be smaller than what it came from.
func (s *state) attempt(c refactor.Candidate, goplsPath string, runTests bool) bool {
	if _, err := refactor.Apply(s.dir, c, goplsPath); err != nil {
		return false
	}
	builds, err := refactor.Repair(s.dir)
	if err != nil || !builds {
		return false
	}
	nodes, err := scoreTree(s.dir)
	if err != nil {
		return false
	}
	if runTests && testsPass(s.dir) != nil {
		return false
	}
	s.nodes = nodes
	s.fingerprint = fingerprint(s.dir)
	return true
}

func copyTried(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// topCandidates lists the most promising changes for a state, rebuilt for that
// state because every applied change moves the code.
func topCandidates(dir, deadcodePath string, hasDeadcode, hasGopls bool, n int, tried map[string]bool) []refactor.Candidate {
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
	}
	var out []refactor.Candidate
	for _, c := range all {
		if c.Predicted <= 0 || tried[c.Key()] {
			continue
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Predicted > out[j].Predicted })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// fingerprint identifies a program by its contents, so two paths that arrive at
// the same code are not explored twice.
func fingerprint(dir string) string {
	h := sha256.New()
	files, _ := goFiles(dir, false)
	sort.Strings(files)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		rel, _ := filepath.Rel(dir, f)
		fmt.Fprintf(h, "%s\x00", rel)
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func copyTree(from, to string) error {
	if err := os.MkdirAll(to, 0o755); err != nil {
		return err
	}
	return filepath.WalkDir(from, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if skipCopy(d.Name()) {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(to, rel), 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(to, rel), b, 0o644)
	})
}

func skipCopy(name string) bool {
	switch name {
	case ".git", "vendor", "testdata":
		return true
	}
	return strings.HasPrefix(name, ".")
}
