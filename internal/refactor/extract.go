package refactor

import (
	"fmt"
	"go/ast"
	"math"
	"sort"

	"github.com/dhilst/sx/internal/cost"
)

// A function heavier than √2·B is worth splitting: moving a run of its
// statements into a function of their own costs a declaration and a call in
// nodes, and takes weight off the caller, the more so the deeper the run sat,
// since the new function holds it at depth one. Nested block bodies are the
// natural cuts, and a long flat function has its runs of top-level
// statements.
//
// Every contiguous run of every statement list is a possible cut. Pricing one
// exactly means working out what the extraction's signature needs, so the
// runs are first ranked by what the weights alone say they would gain, and
// only the best few the model accepts are priced.
const extractShortlist = 16

// Extractions is every function the objective says should be split, each with
// its best cut, reusing what the cache already knows.
func (c *Cache) Extractions(dir string) ([]Candidate, error) {
	dirs, err := packageDirs(dir)
	if err != nil {
		return nil, err
	}
	var out []Candidate
	for _, d := range dirs {
		cs, err := remember(c, d, fmt.Sprintf("extract:%g:%g", cost.Target(), cost.H), nil, func() ([]Candidate, error) {
			return c.extractionsIn(d)
		})
		if err != nil {
			continue
		}
		out = append(out, cs...)
	}
	sortByGain(out)
	return out, nil
}

func (c *Cache) extractionsIn(dir string) ([]Candidate, error) {
	tp, err := c.syntax(dir)
	if err != nil {
		return nil, err
	}
	var paths []string
	for path := range tp.files {
		if !generated(path) {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	var out []Candidate
	for _, path := range paths {
		f := tp.files[path]
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || hasDirective(fn.Doc) {
				continue
			}
			if float64(cost.Weight(fn)) <= math.Sqrt2*cost.Target() {
				continue
			}
			if tp.info == nil {
				if tp, err = c.load(dir); err != nil {
					return nil, nil
				}
				f = tp.files[path]
			}
			if cand, ok := bestCut(tp, path, f, fn); ok {
				out = append(out, cand)
			}
		}
	}
	return out, nil
}

// bestCut is the extraction from fn that lowers the objective most, if any
// does.
func bestCut(tp *typedPackage, path string, f *ast.File, fn *ast.FuncDecl) (Candidate, bool) {
	type cut struct {
		stmts []ast.Stmt
		bound float64 // the gain the weights promise, before the signature
	}
	W := cost.Weight(fn)
	var cuts []cut
	lists(fn.Body, func(list []ast.Stmt) {
		if len(list) == 0 {
			return
		}
		d := max(1, cost.Depth(fn, list[0]))
		for i := range list {
			for j := i + 1; j <= len(list); j++ {
				if d == 1 && i == 0 && j == len(list) {
					continue // the whole body: fn would only wrap the new function
				}
				n, w := cost.Shape(list[i:j])
				// The cheapest extraction: a declaration and a bare call.
				bound := gain(cost.MinOverhead, []int{W}, []int{W - w - (d-1)*n + d, w})
				if bound > 0 {
					cuts = append(cuts, cut{list[i:j], bound})
				}
			}
		}
	})
	sort.SliceStable(cuts, func(i, j int) bool { return cuts[i].bound > cuts[j].bound })
	best, found := Candidate{}, false
	priced := 0
	for i, k := range cuts {
		if priced == extractShortlist || i == 16*extractShortlist {
			break
		}
		// A cut the model refuses does not count: the ones the weights like
		// best are the longest runs, and those are the likeliest to hold
		// something - a defer, an escaping address - that rules them out.
		model, err := predictExtraction(tp, f, k.stmts, 1, []*ast.File{f})
		if err != nil {
			continue
		}
		priced++
		start := tp.fset.Position(k.stmts[0].Pos())
		end := tp.fset.Position(k.stmts[len(k.stmts)-1].End())
		occ := []Occurrence{{
			File: path, StartLine: start.Line, StartCol: start.Column,
			EndLine: end.Line, EndCol: end.Column, Stmts: len(k.stmts),
			StartOffset: start.Offset, EndOffset: end.Offset,
			stmts: k.stmts, file: f,
		}}
		g := extractionGain(model, occ)
		if g <= 0 || found && g <= best.Gain {
			continue
		}
		n, w := cost.Shape(k.stmts)
		best, found = Candidate{
			Kind: KindExtract, File: path, Line: start.Line, Col: start.Column,
			Target:    fmt.Sprintf("%s:%d-%d", cost.FuncName(fn), start.Line, end.Line),
			Predicted: -model.Delta(), Gain: g,
			Occurrences: occ, Hash: hashRun(k.stmts), model: model,
			Detail: fmt.Sprintf("%s weighs %d; lines %d-%d, %d statements weighing %d at depth one: %s",
				cost.FuncName(fn), W, start.Line, end.Line, n, w, model),
		}, true
	}
	return best, found
}

// lists calls visit with every statement list in body, outside function
// literals: the body itself and those of its blocks and clauses.
func lists(body *ast.BlockStmt, visit func([]ast.Stmt)) {
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.BlockStmt:
			visit(n.List)
		case *ast.CaseClause:
			visit(n.Body)
		case *ast.CommClause:
			visit(n.Body)
		}
		return true
	})
}
