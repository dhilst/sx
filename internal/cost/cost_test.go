package cost

import (
	"go/parser"
	"go/token"
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
			// Flattening nesting costs nodes; whether it pays is for the
			// objective to say (see TestObjectiveRewardsFlatterCode).
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

// Comments are not code. A file parsed with its comments measures the same as
// one parsed without them.
func TestCommentsParsedWithTheFileAreNotCounted(t *testing.T) {
	src := "package p\n\n// F does\n// something.\nfunc F() int {\n\t// one\n\treturn 1 // two\n}\n"
	with, err := parser.ParseFile(token.NewFileSet(), "p.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	without, err := parser.ParseFile(token.NewFileSet(), "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	if a, b := Count(with), Count(without); a != b {
		t.Fatalf("with comments %d nodes, without %d", a, b)
	}
}

func weightOf(t *testing.T, src string) int {
	t.Helper()
	f := scoreSrc(t, "package p\n\n"+src+"\n")
	if len(f.Functions) != 1 {
		t.Fatalf("want one function, got %d", len(f.Functions))
	}
	return f.Functions[0].Weight
}

// A block with no nested blocks weighs its statements times its depth, and
// a nested block adds its own.
func TestWeight(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"empty", "func F() {}", 0},
		{"flat", "func F() { a := 1; b := a; _ = b }", 3},
		{"if", "func F(x int) { if x > 0 { x++; x++ } }", 1 + 2*2},
		{"if else", "func F(x int) { if x > 0 { x++ } else { x-- } }", 1 + 2 + 2},
		{"else if stays at its if's depth", "func F(x int) { if x > 0 { x++ } else if x < 0 { x-- } }", 1 + 2 + 1 + 2},
		{"nested", "func F(xs []int) { for _, x := range xs { if x > 0 { x++ } } }", 1 + 2 + 3},
		{"switch", "func F(x int) { switch x { case 1: x++; case 2: x--; x-- } }", 1 + 2 + 2*2},
		{"select", "func F(c chan int) { select { case <-c: c <- 1 } }", 1 + 2},
		{"bare block", "func F(x int) { { x++ } }", 2},
		{"label", "func F() { L: for { break L } }", 1 + 2},
		{"func literal", "func F() { f := func() { println(); println() }; f() }", 1 + 2*2 + 1},
		{"empty statement", "func F() { ; }", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := weightOf(t, c.src); got != c.want {
				t.Errorf("weight %d, want %d", got, c.want)
			}
		})
	}
}

// H is what one more function costs: its declaration and the call to it.
func TestHIsTheCostOfAFunction(t *testing.T) {
	decl, err := parser.ParseFile(token.NewFileSet(), "p.go", "package p\nfunc f() {}", 0)
	if err != nil {
		t.Fatal(err)
	}
	call, err := parser.ParseExpr("f()")
	if err != nil {
		t.Fatal(err)
	}
	// The file itself and its package name are not part of the function; the
	// call is an ExprStmt holding the CallExpr.
	got := Count(decl.Decls[0]) + 1 + Count(call)
	if got != MinOverhead {
		t.Fatalf("a function and its call cost %d nodes, MinOverhead is %d", got, MinOverhead)
	}
}

// At the optimum the average function weighs B: splitting a weight into k
// equal functions, the objective is least at the k for which W/k is B.
func TestTheOptimumAveragesB(t *testing.T) {
	const total = 400
	best, bestK := 0.0, 0
	for k := 1; k <= total; k++ {
		weights := make([]int, k)
		for i := range weights {
			weights[i] = total / k
		}
		weights[0] += total % k
		j := Objective(int(H)*k, weights)
		if bestK == 0 || j < best {
			best, bestK = j, k
		}
	}
	if avg := float64(total) / float64(bestK); avg < Block/1.5 || avg > Block*1.5 {
		t.Fatalf("the optimum splits %d into %d functions of %.1f, B is %.0f", total, bestK, avg, Block)
	}
}

// Inverting a guard costs nodes but takes statements out of a level, and in a
// heavy enough function the objective pays for it.
func TestObjectiveRewardsFlatterCode(t *testing.T) {
	var body, flat string
	for range 20 {
		body += "\t\t\tn += x\n"
		flat += "\t\tn += x\n"
	}
	nested := scoreSrc(t, "package p\n\nfunc F(xs []int) int {\n\tn := 0\n\tfor _, x := range xs {\n\t\tif x > 0 {\n"+body+"\t\t}\n\t}\n\treturn n\n}\n")
	guarded := scoreSrc(t, "package p\n\nfunc F(xs []int) int {\n\tn := 0\n\tfor _, x := range xs {\n\t\tif !(x > 0) {\n\t\t\tcontinue\n\t\t}\n"+flat+"\t}\n\treturn n\n}\n")
	a, b := Objective(nested.Nodes, nested.Weights()), Objective(guarded.Nodes, guarded.Weights())
	if b >= a {
		t.Fatalf("the guard did not pay: J %.1f -> %.1f (nodes %d -> %d, weight %d -> %d)",
			a, b, nested.Nodes, guarded.Nodes, nested.Functions[0].Weight, guarded.Functions[0].Weight)
	}
}
