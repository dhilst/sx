package cost

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func score(t *testing.T, src string) []Function {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "x.go")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	fns, err := ScoreFile(path, DefaultWeights())
	if err != nil {
		t.Fatal(err)
	}
	return fns
}

func total(fns []Function) int {
	sum := 0
	for _, f := range fns {
		sum += f.Total
	}
	return sum
}

// A function inside every allowance is free. If ordinary code is charged, the
// number stops meaning anything.
func TestSmallFunctionCostsNothing(t *testing.T) {
	fns := score(t, `package p

func Add(a, b int) int {
	sum := a + b
	if sum > 100 {
		return 100
	}
	return sum
}
`)
	if len(fns) != 1 {
		t.Fatalf("got %d functions", len(fns))
	}
	if fns[0].Total != 0 {
		t.Errorf("a short, flat, two-argument function cost %d: %+v", fns[0].Total, fns[0])
	}
}

// Each dimension must be monotonic: more of the thing never costs less.
func TestEachDimensionIsMonotonic(t *testing.T) {
	t.Run("arguments", func(t *testing.T) {
		prev := -1
		for n := 1; n <= 8; n++ {
			var params []string
			for i := 0; i < n; i++ {
				params = append(params, fmt.Sprintf("a%d int", i))
			}
			fns := score(t, fmt.Sprintf("package p\n\nfunc F(%s) int { return 0 }\n", strings.Join(params, ", ")))
			got := fns[0].Total
			if got < prev {
				t.Fatalf("%d arguments cost %d, fewer than %d arguments", n, got, n-1)
			}
			prev = got
		}
	})

	t.Run("locals", func(t *testing.T) {
		prev := -1
		for n := 1; n <= 10; n++ {
			var body strings.Builder
			for i := 0; i < n; i++ {
				fmt.Fprintf(&body, "\tv%d := %d\n\t_ = v%d\n", i, i, i)
			}
			fns := score(t, "package p\n\nfunc F() {\n"+body.String()+"}\n")
			got := fns[0].LocalsCost
			if got < prev {
				t.Fatalf("%d locals cost %d, fewer than %d locals", n, got, n-1)
			}
			prev = got
		}
	})
}

// Depth is the point of the model: the same statements buried deeper must cost
// more than the same statements laid flat.
func TestNestingCostsMoreThanTheSameCodeFlat(t *testing.T) {
	flat := score(t, `package p

func F(a, b, c bool) int {
	x := 0
	x++
	x++
	x++
	return x
}
`)
	nested := score(t, `package p

func F(a, b, c bool) int {
	x := 0
	if a {
		if b {
			if c {
				x++
				x++
				x++
			}
		}
	}
	return x
}
`)
	if !(nested[0].NestingCost > flat[0].NestingCost) {
		t.Fatalf("nested cost %d, flat cost %d: burying code must cost more",
			nested[0].NestingCost, flat[0].NestingCost)
	}
	if flat[0].Total != 0 {
		t.Errorf("the flat version should be free, cost %d", flat[0].Total)
	}
}

// One deeply buried line is a curiosity; many are the problem. Charging per
// buried statement rather than by peak depth is what distinguishes them.
func TestManyBuriedStatementsCostMoreThanOne(t *testing.T) {
	one := score(t, `package p

func F(a bool) {
	if a {
		if a {
			if a {
				println(1)
			}
		}
	}
}
`)
	many := score(t, `package p

func F(a bool) {
	if a {
		if a {
			if a {
				println(1)
				println(2)
				println(3)
				println(4)
				println(5)
			}
		}
	}
}
`)
	if one[0].MaxDepth != many[0].MaxDepth {
		t.Fatalf("the two should reach the same depth, got %d and %d", one[0].MaxDepth, many[0].MaxDepth)
	}
	if !(many[0].NestingCost > one[0].NestingCost) {
		t.Fatalf("five buried statements cost %d, one costs %d", many[0].NestingCost, one[0].NestingCost)
	}
}

// The model has to reward the refactoring it is meant to encourage: pulling a
// long, deep function apart should lower the total, and the cost of the new
// function's parameters is what stops that being free.
func TestExtractingAFunctionLowersTheTotal(t *testing.T) {
	before := score(t, `package p

func Process(items []int, limit int) int {
	total := 0
	for _, item := range items {
		if item > limit {
			scaled := item * 2
			if scaled > 100 {
				scaled = 100
			}
			if scaled%2 == 0 {
				total += scaled
			} else {
				total += scaled - 1
			}
		}
	}
	return total
}
`)
	after := score(t, `package p

func scale(item int) int {
	scaled := item * 2
	if scaled > 100 {
		scaled = 100
	}
	if scaled%2 == 0 {
		return scaled
	}
	return scaled - 1
}

func Process(items []int, limit int) int {
	total := 0
	for _, item := range items {
		if item > limit {
			total += scale(item)
		}
	}
	return total
}
`)
	if !(total(after) < total(before)) {
		t.Fatalf("extraction did not pay: %d before, %d after", total(before), total(after))
	}
}

// And the counterweight: extracting into a function that needs a long
// parameter list must not be free, or the model would reward shredding code
// into pieces that are individually small and collectively worse.
func TestExtractionIsNotFreeWhenItNeedsManyParameters(t *testing.T) {
	fns := score(t, `package p

func helper(a, b, c, d, e, f int) int {
	return a + b + c + d + e + f
}
`)
	if fns[0].ParamsCost == 0 {
		t.Fatal("a six-parameter helper should be charged for its parameters")
	}
	if fns[0].Total != fns[0].ParamsCost {
		t.Errorf("its only cost should be the parameters, got %+v", fns[0])
	}
}

func TestFunctionLiteralsAreCountedInTheirEnclosingFunction(t *testing.T) {
	fns := score(t, `package p

func F(xs []int) {
	each(xs, func(x int) {
		if x > 0 {
			if x > 1 {
				println(x)
			}
		}
	})
}

func each(xs []int, fn func(int)) {}
`)
	var f Function
	for _, x := range fns {
		if x.Name == "F" {
			f = x
		}
	}
	if f.MaxDepth < 3 {
		t.Errorf("statements inside a closure should count as nested, got depth %d", f.MaxDepth)
	}
}

// The four dimensions have to be commensurate: being twice over the allowance
// should cost the same whichever allowance it is. Charging the raw difference
// instead of the ratio made length swamp everything, since a function is
// allowed 25 statements and 3 parameters.
func TestDimensionsAreComparableAtEqualOvershoot(t *testing.T) {
	w := DefaultWeights()
	w.StatementWeight, w.LocalWeight, w.ParamWeight, w.DepthWeight = 1, 1, 1, 1

	twiceLength := overshoot(2*w.MaxStatements, w.MaxStatements, w.StatementWeight)
	twiceLocals := overshoot(2*w.MaxLocals, w.MaxLocals, w.LocalWeight)
	twiceParams := overshoot(2*w.MaxParams, w.MaxParams, w.ParamWeight)
	if twiceLength != twiceLocals || twiceLocals != twiceParams {
		t.Fatalf("equal overshoot should cost equally: length %d, locals %d, params %d",
			twiceLength, twiceLocals, twiceParams)
	}
	if twiceLength == 0 {
		t.Fatal("twice the allowance should not be free")
	}
	// And the growth is superlinear: four times over costs more than twice
	// twice-over.
	if overshoot(4*w.MaxParams, w.MaxParams, 1) <= 2*twiceParams {
		t.Error("cost should grow faster than linearly in the overshoot")
	}
}
