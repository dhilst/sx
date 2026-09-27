package refactor

import (
	"go/ast"
	"sort"

	"github.com/dhilst/sx/internal/cost"
)

// A change moves the objective J = S + H·Σ(W/B)² by its change in nodes and
// by what it does to the weights of the functions it touches: the ones it
// changes or removes weigh before, the ones it changes or adds weigh after.

// gain is the predicted fall in J for a change of delta nodes that turns
// functions weighing before into functions weighing after.
func gain(delta int, before, after []int) float64 {
	g := float64(-delta)
	for _, w := range before {
		g += cost.Load(w)
	}
	for _, w := range after {
		g -= cost.Load(w)
	}
	return g
}

// enclosing is the declaration in file that holds pos.
func enclosing(file *ast.File, pos ast.Node) *ast.FuncDecl {
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil && fn.Pos() <= pos.Pos() && pos.End() <= fn.End() {
			return fn
		}
	}
	return nil
}

// extractionGain is what extracting occ into one function called from each
// copy does to J. A run of n statements weighing w at depth one leaves a
// caller that held it at depth d lighter by w + (d-1)·n, and heavier by the
// call site; the new function holds the run at depth one.
func extractionGain(e Extraction, occ []Occurrence) float64 {
	n, w := cost.Shape(occ[0].stmts)
	callers := map[*ast.FuncDecl]int{}
	var order []*ast.FuncDecl
	for _, o := range occ {
		fn := enclosing(o.file, o.stmts[0])
		if fn == nil {
			continue
		}
		if _, ok := callers[fn]; !ok {
			callers[fn] = cost.Weight(fn)
			order = append(order, fn)
		}
		d := max(1, cost.Depth(fn, o.stmts[0])) // 0 for a labelled statement
		callers[fn] -= w + (d-1)*n - (e.Site*d + e.Deep)
	}
	var before, after []int
	for _, fn := range order {
		before = append(before, cost.Weight(fn))
		after = append(after, callers[fn])
	}
	after = append(after, w+e.Added)
	return gain(e.Delta(), before, after)
}

// sortByGain orders candidates by the fall in J they promise, most first.
func sortByGain(cands []Candidate) {
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].Gain > cands[j].Gain })
}
