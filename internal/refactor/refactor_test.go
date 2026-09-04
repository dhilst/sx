package refactor

import (
	"os"
	"path/filepath"
	"testing"

	"purgatrix/internal/cost"
)

func write(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "x.go")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// A block that returns, breaks or continues cannot be lifted into another
// function: the control flow means something different once it is somewhere
// else. Offering such a block to gopls wastes a round at best.
func TestBlocksThatEscapeAreNotCandidates(t *testing.T) {
	cases := map[string]string{
		"return": `package p

func F(xs []int, limit int, factor int) int {
	total := 0
	for _, x := range xs {
		if x > limit {
			a := x * factor
			b := a + 1
			c := b * 2
			if c > 10 {
				return c
			}
			total += c
		}
	}
	return total
}
`,
		"break": `package p

func F(xs []int, limit int, factor int) int {
	total := 0
	for _, x := range xs {
		if x > limit {
			a := x * factor
			b := a + 1
			c := b * 2
			total += c
			break
		}
	}
	return total
}
`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := Candidates(write(t, src), cost.DefaultWeights(), 3)
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range got {
				if c.StartLine <= 7 && c.EndLine >= 10 {
					t.Errorf("offered a block containing an escaping %s: lines %d-%d", name, c.StartLine, c.EndLine)
				}
			}
		})
	}
}

// A block worth extracting is offered, with a range that names its statements
// rather than the braces around them.
func TestNestedBlockIsOfferedWithItsOwnRange(t *testing.T) {
	src := `package p

func F(xs []int, limit int, factor int) int {
	total := 0
	for _, x := range xs {
		if x > limit {
			a := x * factor
			b := a + 1
			c := b * 2
			total += c
		}
	}
	return total
}
`
	got, err := Candidates(write(t, src), cost.DefaultWeights(), 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("no candidate offered for a four-statement block at depth 3")
	}
	c := got[0]
	if c.Function != "F" {
		t.Errorf("candidate attributed to %q", c.Function)
	}
	if c.StartLine != 7 || c.EndLine != 10 {
		t.Errorf("range is lines %d-%d, want 7-10 (the statements, not the braces)", c.StartLine, c.EndLine)
	}
	if c.Depth < 3 {
		t.Errorf("depth recorded as %d", c.Depth)
	}
}

// Functions already inside every allowance are left alone: there is nothing to
// pay for, so an extraction could only add a call and some parameters.
func TestFunctionsWithinTheAllowancesAreNotTouched(t *testing.T) {
	src := `package p

func Small(a, b int) int {
	if a > b {
		return a
	}
	return b
}
`
	got, err := Candidates(write(t, src), cost.DefaultWeights(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("offered %d candidates in a function that costs nothing", len(got))
	}
}

func TestAvailableFindsGoplsOrSaysSo(t *testing.T) {
	path, ok := Available()
	if ok && path == "" {
		t.Fatal("reported gopls available with no path")
	}
	if !ok {
		t.Skip("gopls is not installed")
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("reported gopls at %q: %v", path, err)
	}
}
