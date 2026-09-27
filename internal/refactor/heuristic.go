package refactor

import (
	"fmt"
	"go/ast"
	"sort"

	"github.com/dhilst/sx/internal/cost"
)

// A heuristic proposes a change by rule: a shape of code that is worth
// changing whenever it is found, rather than the best of every cut the
// search can think of. What a rule proposes is still priced by the models and
// kept only if the measured J falls; a rule only decides where to look.
type heuristic struct {
	name string
	// propose returns the runs of statements in fn to move into functions
	// of their own, and whether each may take its defers along.
	propose func(fn *ast.FuncDecl) []proposal
}

type proposal struct {
	stmts []ast.Stmt
	tail  bool // the run ends by returning from fn: its defers may move
}

// heuristics is every rule, in the order they are tried.
var heuristics = []heuristic{
	{"extract-heavy-block", heavyBlocks},
}

// heavyBlocks proposes the body of each block heavier than mB - an if or
// else branch, a for or range loop, a case of a switch or select: a body that
// big is a function of its own. One that ends by returning takes its defers
// along - the subcommand at the top of a command's run(), say, which the
// search could not move because of the files it closes on the way out. A
// loop body is priced like any run, so one whose break or continue would
// leave it is refused.
func heavyBlocks(fn *ast.FuncDecl) []proposal {
	var out []proposal
	add := func(list []ast.Stmt) {
		if len(list) == 0 {
			return
		}
		if _, w := cost.Shape(list); float64(w) > cost.Target() {
			_, tail := list[len(list)-1].(*ast.ReturnStmt)
			out = append(out, proposal{list, tail})
		}
	}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.IfStmt:
			add(n.Body.List)
			if e, ok := n.Else.(*ast.BlockStmt); ok {
				add(e.List)
			}
		case *ast.ForStmt:
			add(n.Body.List)
		case *ast.RangeStmt:
			add(n.Body.List)
		case *ast.CaseClause:
			add(n.Body)
		case *ast.CommClause:
			add(n.Body)
		}
		return true
	})
	return out
}

// Heuristics is every change the rules propose that the models say lowers
// J, reusing what the cache already knows.
func (c *Cache) Heuristics(dir string) ([]Candidate, error) {
	dirs, err := packageDirs(dir)
	if err != nil {
		return nil, err
	}
	var out []Candidate
	for _, d := range dirs {
		cs, err := remember(c, d, fmt.Sprintf("heuristic:%g:%g", cost.Target(), cost.H), nil, func() ([]Candidate, error) {
			return c.heuristicsIn(d)
		})
		if err != nil {
			continue
		}
		out = append(out, cs...)
	}
	sortByGain(out)
	return out, nil
}

func (c *Cache) heuristicsIn(dir string) ([]Candidate, error) {
	tp, err := c.load(dir)
	if err != nil {
		return nil, nil
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
			for _, h := range heuristics {
				for _, p := range h.propose(fn) {
					if cand, ok := priceProposal(tp, path, f, fn, h.name, p); ok {
						out = append(out, cand)
					}
				}
			}
		}
	}
	return out, nil
}

// priceProposal prices moving p out of fn, and makes it a candidate if the
// models say J falls.
func priceProposal(tp *typedPackage, path string, f *ast.File, fn *ast.FuncDecl, rule string, p proposal) (Candidate, bool) {
	model, err := predictExtractionTail(tp, f, p.stmts, 1, []*ast.File{f}, p.tail)
	if err != nil {
		return Candidate{}, false
	}
	start := tp.fset.Position(p.stmts[0].Pos())
	end := tp.fset.Position(p.stmts[len(p.stmts)-1].End())
	occ := []Occurrence{{
		File: path, StartLine: start.Line, StartCol: start.Column,
		EndLine: end.Line, EndCol: end.Column, Stmts: len(p.stmts),
		StartOffset: start.Offset, EndOffset: end.Offset,
		stmts: p.stmts, file: f,
	}}
	g := extractionGain(model, occ)
	if g <= 0 {
		return Candidate{}, false
	}
	n, w := cost.Shape(p.stmts)
	return Candidate{
		Kind: KindHeuristic, File: path, Line: start.Line, Col: start.Column,
		Target:    fmt.Sprintf("%s:%s:%d-%d", rule, cost.FuncName(fn), start.Line, end.Line),
		Predicted: -model.Delta(), Gain: g,
		Occurrences: occ, Hash: hashRun(p.stmts), model: model,
		Detail: fmt.Sprintf("%s: in %s (weight %d), lines %d-%d, %d statements weighing %d at depth one: %s",
			rule, cost.FuncName(fn), cost.Weight(fn), start.Line, end.Line, n, w, model),
	}, true
}
