package refactor

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, name, src string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Repeated code is found by hashing runs of statements, not whole blocks:
// duplication rarely lines up with a brace.
func TestDuplicateRunsAreFoundInsideBlocks(t *testing.T) {
	dir := write(t, "p.go", `package p

func A(xs []int) int {
	total := 0
	for _, x := range xs {
		scaled := x * 2
		adjusted := scaled + 1
		total += adjusted
		total += scaled
	}
	return total
}

func B(xs []int) int {
	total := 0
	for _, x := range xs {
		scaled := x * 2
		adjusted := scaled + 1
		total += adjusted
		total += scaled
	}
	return total * 2
}
`)
	got, err := DuplicateCandidates(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("the repeated loop body was not found")
	}
	c := got[0]
	if len(c.Occurrences) != 2 {
		t.Errorf("found %d occurrences, want 2", len(c.Occurrences))
	}
	if c.Predicted <= 0 {
		t.Errorf("a repeat worth factoring should predict a saving, got %d", c.Predicted)
	}
	if c.Hash == "" {
		t.Error("a duplicate must carry its hash, or the copies cannot be found again after the first is extracted")
	}
}

// Code that merely looks alike is not a duplicate. The hash includes
// identifiers and literals, so a difference in either is a difference.
func TestDifferentIdentifiersAreNotDuplicates(t *testing.T) {
	dir := write(t, "p.go", `package p

func A(xs []int) int {
	total := 0
	for _, x := range xs {
		scaled := x * 2
		adjusted := scaled + 1
		total += adjusted
		total += scaled
	}
	return total
}

func B(ys []int) int {
	sum := 0
	for _, y := range ys {
		doubled := y * 3
		bumped := doubled + 9
		sum += bumped
		sum += doubled
	}
	return sum
}
`)
	got, err := DuplicateCandidates(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("same shape with different names was reported as duplication: %+v", got[0].Detail)
	}
}

// Two copies in different packages cannot share a function without exporting
// and importing it, so they are not offered as one candidate.
func TestDuplicatesAreNotGroupedAcrossPackages(t *testing.T) {
	dir := t.TempDir()
	body := `	total := 0
	for _, x := range xs {
		scaled := x * 2
		adjusted := scaled + 1
		total += adjusted
		total += scaled
	}
	return total
`
	for _, pkg := range []string{"one", "two"} {
		sub := filepath.Join(dir, pkg)
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		src := "package " + pkg + "\n\nfunc F(xs []int) int {\n" + body + "}\n"
		if err := os.WriteFile(filepath.Join(sub, "p.go"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := DuplicateCandidates(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range got {
		dirs := map[string]bool{}
		for _, o := range c.Occurrences {
			dirs[filepath.Dir(o.File)] = true
		}
		if len(dirs) > 1 {
			t.Errorf("a candidate spans %d packages", len(dirs))
		}
	}
}

// A duplicate is identified by what it is, not where: every applied change
// moves the lines below it, and a position-based key let one rejected
// candidate come back seven times in a single run.
func TestDuplicateKeyIsStableUnderMovement(t *testing.T) {
	a := Candidate{Kind: KindDuplicate, Hash: "abc123", File: "x.go", Line: 10}
	b := Candidate{Kind: KindDuplicate, Hash: "abc123", File: "x.go", Line: 40}
	if a.Key() != b.Key() {
		t.Errorf("the same duplicate got two keys: %q and %q", a.Key(), b.Key())
	}
	c := Candidate{Kind: KindInline, Target: "f", File: "x.go", Line: 10}
	if c.Key() == a.Key() {
		t.Error("different kinds must not share a key")
	}
}

// An unused import is the one build failure worth repairing: nothing can
// depend on it. Anything else is the transformation being wrong, not unfinished.
func TestOnlyUnusedImportsAreConsideredRepairable(t *testing.T) {
	if !(BuildProblem{Message: `"fmt" imported and not used`}).Repairable() {
		t.Error("an unused import should be repairable")
	}
	for _, msg := range []string{"undefined: helper", "declared and not used: x", "too many return values"} {
		if (BuildProblem{Message: msg}).Repairable() {
			t.Errorf("%q should not be treated as repairable", msg)
		}
	}
}
