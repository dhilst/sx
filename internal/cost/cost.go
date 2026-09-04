// Package cost models the structural complexity of Go functions.
//
// Four things are charged: how long a function is, how many locals it holds in
// the reader's head, how many arguments it takes, and how deeply its code is
// nested. Each has a free allowance, because a function with three parameters
// is not complicated, and each grows faster than linearly beyond it, because
// the eighth parameter is worse than the fifth.
package cost

import (
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"path/filepath"
	"sort"
)

// Weights and allowances. These are the whole model: everything else is
// counting. They are exported so a caller can try a different shape without
// editing the package.
type Weights struct {
	// Free allowances. Below these, a dimension costs nothing.
	MaxStatements int
	MaxLocals     int
	MaxParams     int
	MaxDepth      int

	// Per-unit weights applied to the squared excess.
	StatementWeight int
	LocalWeight     int
	ParamWeight     int
	DepthWeight     int
}

func DefaultWeights() Weights {
	return Weights{
		MaxStatements: 25,
		MaxLocals:     5,
		MaxParams:     3,
		MaxDepth:      2,

		StatementWeight: 1,
		LocalWeight:     2,
		ParamWeight:     3,
		DepthWeight:     4,
	}
}

// Function is one scored function.
type Function struct {
	Name       string `json:"name"`
	File       string `json:"file"`
	Line       int    `json:"line"`
	Statements int    `json:"statements"`
	Locals     int    `json:"locals"`
	Params     int    `json:"params"`
	MaxDepth   int    `json:"max_depth"`
	DeepStmts  int    `json:"deep_statements"`

	LengthCost  int `json:"length_cost"`
	LocalsCost  int `json:"locals_cost"`
	ParamsCost  int `json:"params_cost"`
	NestingCost int `json:"nesting_cost"`
	Total       int `json:"total"`
}

// Report is a scored set of files.
type Report struct {
	Total     int        `json:"total"`
	Functions []Function `json:"functions"`
}

// ScoreFile parses one Go file and scores every function in it.
func ScoreFile(path string, w Weights) ([]Function, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, err
	}
	var out []Function
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		out = append(out, scoreFunc(fset, path, fn, w))
	}
	return out, nil
}

func scoreFunc(fset *token.FileSet, path string, fn *ast.FuncDecl, w Weights) Function {
	f := Function{
		Name:   funcName(fn),
		File:   filepath.ToSlash(path),
		Line:   fset.Position(fn.Pos()).Line,
		Params: countParams(fn),
	}
	f.Statements, f.Locals, f.MaxDepth, f.DeepStmts = walk(fn.Body, w)

	f.LengthCost = overshoot(f.Statements, w.MaxStatements, w.StatementWeight)
	f.LocalsCost = overshoot(f.Locals, w.MaxLocals, w.LocalWeight)
	f.ParamsCost = overshoot(f.Params, w.MaxParams, w.ParamWeight)
	// Nesting is charged on how buried the function is on average, not on its
	// peak: one deep line is a curiosity, a function whose every statement sits
	// four levels down is the problem.
	f.NestingCost = 0
	if f.Statements > 0 {
		avgDepth := 1 + float64(f.DeepStmts)/float64(f.Statements) + float64(w.MaxDepth) - 1
		f.NestingCost = overshootF(avgDepth, float64(w.MaxDepth), w.DepthWeight)
	}
	f.Total = f.LengthCost + f.LocalsCost + f.ParamsCost + f.NestingCost
	return f
}

// overshoot charges nothing up to the allowance, then grows with the square of
// how many times over it the function is.
//
// Measuring the overshoot as a ratio rather than a difference is what keeps the
// four dimensions comparable. Charging the raw difference made length dominate
// everything - a 156-statement function scored 17161 for its length and 996 for
// being buried seven levels deep, which is the wrong way round.
func overshoot(n, free, weight int) int {
	return overshootF(float64(n), float64(free), weight)
}

func overshootF(n, free float64, weight int) int {
	if free <= 0 || n <= free {
		return 0
	}
	ratio := n / free
	return int(math.Round(float64(weight) * ratio * ratio))
}

// walk counts statements, distinct locals, peak depth, and how many statements
// sit below the allowed depth.
func walk(body *ast.BlockStmt, w Weights) (statements, locals, maxDepth, deepStmts int) {
	seen := map[string]bool{}
	var visit func(n ast.Node, depth int)
	visit = func(n ast.Node, depth int) {
		if n == nil {
			return
		}
		if depth > maxDepth {
			maxDepth = depth
		}
		if stmt, ok := n.(ast.Stmt); ok {
			if _, isBlock := stmt.(*ast.BlockStmt); !isBlock {
				statements++
				if over := depth - w.MaxDepth; over > 0 {
					deepStmts += over
				}
			}
		}
		switch s := n.(type) {
		case *ast.AssignStmt:
			if s.Tok == token.DEFINE {
				for _, lhs := range s.Lhs {
					if id, ok := lhs.(*ast.Ident); ok && id.Name != "_" && !seen[id.Name] {
						seen[id.Name] = true
						locals++
					}
				}
			}
		case *ast.DeclStmt:
			if gen, ok := s.Decl.(*ast.GenDecl); ok && gen.Tok == token.VAR {
				for _, spec := range gen.Specs {
					if vs, ok := spec.(*ast.ValueSpec); ok {
						for _, name := range vs.Names {
							if name.Name != "_" && !seen[name.Name] {
								seen[name.Name] = true
								locals++
							}
						}
					}
				}
			}
		case *ast.RangeStmt:
			for _, e := range []ast.Expr{s.Key, s.Value} {
				if id, ok := e.(*ast.Ident); ok && id.Name != "_" && !seen[id.Name] {
					seen[id.Name] = true
					locals++
				}
			}
		}
		// Only these constructs bury the code inside them.
		inner := depth
		switch n.(type) {
		case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt,
			*ast.TypeSwitchStmt, *ast.SelectStmt, *ast.FuncLit:
			inner = depth + 1
		}
		for _, child := range children(n) {
			visit(child, inner)
		}
	}
	for _, stmt := range body.List {
		visit(stmt, 1)
	}
	return statements, locals, maxDepth, deepStmts
}

// children returns the statement-bearing children of a node. Expressions are
// walked only far enough to find function literals, which carry statements of
// their own.
func children(n ast.Node) []ast.Node {
	var out []ast.Node
	add := func(nodes ...ast.Node) {
		for _, c := range nodes {
			if c != nil && !isNilNode(c) {
				out = append(out, c)
			}
		}
	}
	switch s := n.(type) {
	case *ast.BlockStmt:
		for _, x := range s.List {
			add(x)
		}
	case *ast.IfStmt:
		add(s.Init, s.Body, s.Else)
	case *ast.ForStmt:
		add(s.Init, s.Post, s.Body)
	case *ast.RangeStmt:
		add(s.Body)
	case *ast.SwitchStmt:
		add(s.Init, s.Body)
	case *ast.TypeSwitchStmt:
		add(s.Init, s.Assign, s.Body)
	case *ast.CaseClause:
		for _, x := range s.Body {
			add(x)
		}
	case *ast.SelectStmt:
		add(s.Body)
	case *ast.CommClause:
		add(s.Comm)
		for _, x := range s.Body {
			add(x)
		}
	case *ast.LabeledStmt:
		add(s.Stmt)
	case *ast.DeferStmt:
		add(s.Call)
	case *ast.GoStmt:
		add(s.Call)
	case *ast.ExprStmt:
		add(s.X)
	case *ast.AssignStmt:
		for _, x := range s.Rhs {
			add(x)
		}
	case *ast.ReturnStmt:
		for _, x := range s.Results {
			add(x)
		}
	case *ast.CallExpr:
		add(s.Fun)
		for _, x := range s.Args {
			add(x)
		}
	case *ast.FuncLit:
		add(s.Body)
	}
	return out
}

func isNilNode(n ast.Node) bool {
	switch v := n.(type) {
	case *ast.BlockStmt:
		return v == nil
	case ast.Stmt:
		return v == nil
	case ast.Expr:
		return v == nil
	}
	return false
}

func countParams(fn *ast.FuncDecl) int {
	n := 0
	if fn.Type.Params != nil {
		for _, f := range fn.Type.Params.List {
			if len(f.Names) == 0 {
				n++
				continue
			}
			n += len(f.Names)
		}
	}
	return n
}

func funcName(fn *ast.FuncDecl) string {
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

// Sort orders functions by cost, worst first.
func Sort(fns []Function) {
	sort.SliceStable(fns, func(i, j int) bool {
		if fns[i].Total != fns[j].Total {
			return fns[i].Total > fns[j].Total
		}
		if fns[i].File != fns[j].File {
			return fns[i].File < fns[j].File
		}
		return fns[i].Line < fns[j].Line
	})
}
