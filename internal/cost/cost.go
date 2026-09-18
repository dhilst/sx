// Package cost measures the size of Go code as the number of AST nodes it
// takes to express it.
//
// The objective is min |AST| subject to behaviour: the same outputs and side
// effects for the same inputs and state. Counting nodes rather than lines makes
// the measure independent of formatting, and rather than weighing dimensions
// against one another it has no parameters at all - so there is nothing to tune
// and nothing to validate against anyone's judgement.
//
// The measure is additive: a node costs one wherever it sits. That is the
// property the previous weighted model lacked, and it is what makes the number
// usable as a target rather than only as a ranking.
package cost

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
)

// Function is one scored function.
type Function struct {
	Name  string `json:"name"`
	File  string `json:"file"`
	Line  int    `json:"line"`
	Nodes int    `json:"nodes"`
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
			Name:  FuncName(fn),
			File:  out.Path,
			Line:  fset.Position(fn.Pos()).Line,
			Nodes: Count(fn),
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
