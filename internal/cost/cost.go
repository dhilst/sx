// Package cost measures Go code by two terms: its size, S, the number of AST
// nodes it takes to express it, and the reading load of its functions.
//
// S alone rewards inlining everything into long functions. So each function
// also pays for its weight W, the sum over its statements of how deeply each
// is nested - for a block with no nested blocks, statements × depth:
//
//	J = S + H·Σ_f (W_f/B)²
//
// H is what one more function costs in nodes (a declaration and a call) and B
// the weight a function should have. Spreading a total weight over k
// functions costs k·H in declarations and H·W²/(k·B²) in reading, which is
// least when W/k = B: at the optimum the average function weighs B. A move
// that splits a function pays when W > √2·B, and one that merges two pays
// when W_f·W_g < B²/2.
//
// Counting nodes and statements rather than lines keeps the measure
// independent of formatting.
package cost

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
)

// MinOverhead is what extracting a function costs at least: the declaration
// "func f() {}" and the statement "f()" that calls it.
const MinOverhead = 8

// H is what extracting a function costs in nodes, typically. The optimum puts
// the average weight at B·√(h/H) for extractions that really cost h, so H is
// the typical cost - parameters, results, the error check at the call - and
// not the least one. On sx itself the extractions worth making cost a median
// of 31 to 37 nodes. The command line sets it.
var H = 32.0

// Block is B, the weight a function should have. The command line sets it.
var Block = 20.0

// M is the multiplier on B: the objective aims at functions weighing M·B. It
// need not be whole. The command line sets it.
var M = 1.0

// Target is M·B, the weight the objective puts the average function at.
func Target() float64 { return M * Block }

// Tree is a measured tree: its size and its objective.
type Tree struct {
	Nodes     int
	Objective float64
}

func (t Tree) String() string { return fmt.Sprintf("%d nodes, J %.1f", t.Nodes, t.Objective) }

// Load is what a function of weight w adds to the objective.
func Load(w int) float64 {
	x := float64(w) / Target()
	return H * x * x
}

// Objective is J for a tree of the given size whose functions weigh weights.
func Objective(nodes int, weights []int) float64 {
	j := float64(nodes)
	for _, w := range weights {
		j += Load(w)
	}
	return j
}

// Function is one scored function.
type Function struct {
	Name  string `json:"name"`
	File  string `json:"file"`
	Line  int    `json:"line"`
	Nodes int    `json:"nodes"`
	// Weight is the sum over its statements of their depth.
	Weight int `json:"weight"`
}

// File is one scored file. Nodes counts the whole file, so declarations that
// belong to no function - imports, types, constants - are counted too.
type File struct {
	Path      string     `json:"path"`
	Nodes     int        `json:"nodes"`
	Functions []Function `json:"functions"`
}

// Report is a scored set of files.
type Report struct {
	Total     int        `json:"total"`
	Objective float64    `json:"objective"`
	Files     []File     `json:"files"`
	Functions []Function `json:"functions"`
}

// ScoreFile counts the nodes in a file and in each of its functions.
func ScoreFile(path string) (File, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return File{}, err
	}
	out := File{Path: filepath.ToSlash(path), Nodes: Count(file)}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		out.Functions = append(out.Functions, Function{
			Name:   FuncName(fn),
			File:   out.Path,
			Line:   fset.Position(fn.Pos()).Line,
			Nodes:  Count(fn),
			Weight: Weight(fn),
		})
	}
	return out, nil
}

// Count is the measure: how many AST nodes this subtree takes. Comments are
// not code, and are not counted even when the file was parsed with them: a
// declaration's doc comment once made a model count thirty lines of prose as
// nodes the change would remove.
func Count(n ast.Node) int {
	total := 0
	ast.Inspect(n, func(node ast.Node) bool {
		switch node.(type) {
		case nil:
		case *ast.CommentGroup, *ast.Comment:
			return false
		default:
			total++
		}
		return true
	})
	return total
}

// Weights is the weight of each function in the file.
func (f File) Weights() []int {
	ws := make([]int, len(f.Functions))
	for i, fn := range f.Functions {
		ws[i] = fn.Weight
	}
	return ws
}

// Weight is the sum over n's statements of their depth. A function's body is
// at depth one; the body of an if, else, for, range, case, select clause or
// function literal sits one deeper than the statement that holds it, and an
// else-if stays at the depth of its if. Blocks, clauses, empty statements and
// labels are structure, not statements, and weigh nothing themselves.
func Weight(n ast.Node) int {
	w := 0
	Visit(n, 1, func(_ ast.Stmt, d int) { w += d })
	return w
}

// Shape is how many statements a list holds and what it weighs at depth one.
// At depth d it weighs weight + (d-1)·count.
func Shape(list []ast.Stmt) (count, weight int) {
	walker(func(_ ast.Stmt, d int) { count, weight = count+1, weight+d }).stmts(list, 1)
	return count, weight
}

// Depth is the depth target sits at in fn, or 0 if it is not there.
func Depth(fn ast.Node, target ast.Stmt) int {
	depth := 0
	Visit(fn, 1, func(s ast.Stmt, d int) {
		if s == target {
			depth = d
		}
	})
	return depth
}

// Visit calls visit with every statement n holds and the depth it sits at,
// taking n's own statements to be at depth d: for a function, its body's.
func Visit(n ast.Node, d int, visit func(s ast.Stmt, d int)) {
	w := walker(visit)
	switch n := n.(type) {
	case *ast.FuncDecl:
		if n.Body != nil {
			w.stmts(n.Body.List, d)
		}
	case *ast.BlockStmt:
		w.stmts(n.List, d)
	case ast.Stmt:
		w.stmt(n, d)
	default:
		w.lits(n, d)
	}
}

type walker func(s ast.Stmt, d int)

func (w walker) stmts(list []ast.Stmt, d int) {
	for _, s := range list {
		w.stmt(s, d)
	}
}

func (w walker) stmt(s ast.Stmt, d int) {
	switch s := s.(type) {
	case nil, *ast.EmptyStmt:
		return
	case *ast.BlockStmt:
		w.stmts(s.List, d+1)
		return
	case *ast.LabeledStmt:
		w.stmt(s.Stmt, d)
		return
	}
	w(s, d)
	switch s := s.(type) {
	case *ast.IfStmt:
		w.lits(s.Init, d)
		w.lits(s.Cond, d)
		w.stmts(s.Body.List, d+1)
		switch e := s.Else.(type) {
		case *ast.IfStmt:
			w.stmt(e, d)
		case *ast.BlockStmt:
			w.stmts(e.List, d+1)
		}
	case *ast.ForStmt:
		w.lits(s.Init, d)
		w.lits(s.Cond, d)
		w.lits(s.Post, d)
		w.stmts(s.Body.List, d+1)
	case *ast.RangeStmt:
		w.lits(s.X, d)
		w.stmts(s.Body.List, d+1)
	case *ast.SwitchStmt:
		w.lits(s.Init, d)
		w.lits(s.Tag, d)
		w.clauses(s.Body, d)
	case *ast.TypeSwitchStmt:
		w.lits(s.Init, d)
		w.lits(s.Assign, d)
		w.clauses(s.Body, d)
	case *ast.SelectStmt:
		w.clauses(s.Body, d)
	default:
		w.lits(s, d)
	}
}

func (w walker) clauses(body *ast.BlockStmt, d int) {
	for _, c := range body.List {
		switch c := c.(type) {
		case *ast.CaseClause:
			for _, e := range c.List {
				w.lits(e, d)
			}
			w.stmts(c.Body, d+1)
		case *ast.CommClause:
			w.lits(c.Comm, d)
			w.stmts(c.Body, d+1)
		}
	}
}

// lits walks the function literals in n, which sits at depth d: their bodies
// read as part of the function around them, one level deeper.
func (w walker) lits(n ast.Node, d int) {
	if n == nil {
		return
	}
	ast.Inspect(n, func(n ast.Node) bool {
		if lit, ok := n.(*ast.FuncLit); ok {
			w.stmts(lit.Body.List, d+1)
			return false
		}
		return true
	})
}

func FuncName(fn *ast.FuncDecl) string {
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		return receiverName(fn.Recv.List[0].Type) + "." + fn.Name.Name
	}
	return fn.Name.Name
}

func receiverName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return receiverName(t.X)
	}
	return "?"
}

// Sort orders functions by size, largest first.
func Sort(fns []Function) {
	sort.SliceStable(fns, func(i, j int) bool {
		if fns[i].Nodes != fns[j].Nodes {
			return fns[i].Nodes > fns[j].Nodes
		}
		if fns[i].File != fns[j].File {
			return fns[i].File < fns[j].File
		}
		return fns[i].Line < fns[j].Line
	})
}
