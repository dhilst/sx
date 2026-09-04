package cost

import (
	"os"
	"path/filepath"
	"testing"
)

func scoreSrc(t *testing.T, src string) File {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "x.go")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := ScoreFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// The measure is formatting-independent: the same program written out
// differently is the same size. That is the point of counting nodes rather
// than lines.
func TestFormattingDoesNotChangeTheCount(t *testing.T) {
	spread := scoreSrc(t, `package p

func F(a int) int {
	if a > 0 {
		return a
	}
	return 0
}
`)
	packed := scoreSrc(t, "package p\n\nfunc F(a int) int {\n\tif a > 0 {\n\t\treturn a\n\t}\n\treturn 0\n}\n")
	if spread.Nodes != packed.Nodes {
		t.Fatalf("same program counted %d and %d", spread.Nodes, packed.Nodes)
	}
}

// Comments are not program structure and must not be counted, or the measure
// would reward deleting them.
func TestCommentsAreNotCounted(t *testing.T) {
	bare := scoreSrc(t, "package p\n\nfunc F() int { return 1 }\n")
	documented := scoreSrc(t, "package p\n\n// F returns one.\n// It is used in the tests.\nfunc F() int { return 1 }\n")
	if bare.Nodes != documented.Nodes {
		t.Fatalf("comments changed the count: %d without, %d with", bare.Nodes, documented.Nodes)
	}
}

// Each primitive has to move the number in the direction the objective says.
func TestPrimitivesMoveTheMeasure(t *testing.T) {
	cases := []struct {
		name          string
		before, after string
		wantSmaller   bool
	}{
		{
			name:        "removing dead code",
			before:      "package p\n\nfunc Live() int { return 1 }\nfunc dead() int { x := 2\n return x }\n",
			after:       "package p\n\nfunc Live() int { return 1 }\n",
			wantSmaller: true,
		},
		{
			name:        "inlining a single-use wrapper",
			before:      "package p\n\nfunc wrap(a int) int { return a + 1 }\nfunc Use(a int) int { return wrap(a) }\n",
			after:       "package p\n\nfunc Use(a int) int { return a + 1 }\n",
			wantSmaller: true,
		},
		{
			name:        "merging nested conditions",
			before:      "package p\n\nfunc F(a, b bool) int {\n\tif a {\n\t\tif b {\n\t\t\treturn 1\n\t\t}\n\t}\n\treturn 0\n}\n",
			after:       "package p\n\nfunc F(a, b bool) int {\n\tif a && b {\n\t\treturn 1\n\t}\n\treturn 0\n}\n",
			wantSmaller: true,
		},
		{
			// Kept as a warning: flattening nesting costs nodes. It was a
			// primitive under the weighted model and is not one under this
			// objective.
			name:        "inverting a guard",
			before:      "package p\n\nfunc F(xs []int) int {\n\tn := 0\n\tfor _, x := range xs {\n\t\tif x > 0 {\n\t\t\ty := x * 2\n\t\t\tn += y\n\t\t}\n\t}\n\treturn n\n}\n",
			after:       "package p\n\nfunc F(xs []int) int {\n\tn := 0\n\tfor _, x := range xs {\n\t\tif !(x > 0) {\n\t\t\tcontinue\n\t\t}\n\t\ty := x * 2\n\t\tn += y\n\t}\n\treturn n\n}\n",
			wantSmaller: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := scoreSrc(t, c.before).Nodes
			after := scoreSrc(t, c.after).Nodes
			if c.wantSmaller && after >= before {
				t.Errorf("%s did not shrink the program: %d -> %d", c.name, before, after)
			}
			if !c.wantSmaller && after <= before {
				t.Errorf("%s was expected to cost nodes, got %d -> %d", c.name, before, after)
			}
		})
	}
}
