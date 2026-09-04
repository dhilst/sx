// Package refactor turns a cost measurement into a source change.
//
// It does not implement refactorings. gopls already does, with type
// information, which is what makes extraction safe: it works out the free
// variables of a block, its return values, and whether the extraction is legal
// at all. What gopls does not have is any idea which extraction is worth doing
// - it will happily extract any selection you name.
//
// That is the part this package supplies: pick the span whose removal the cost
// model says will pay, ask gopls to perform it, then measure whether it did.
package refactor

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"purgatrix/internal/cost"
)

// Candidate is a block that could be lifted into its own function.
type Candidate struct {
	File       string
	Function   string
	StartLine  int
	StartCol   int
	EndLine    int
	EndCol     int
	Statements int
	Depth      int
	// Predicted is what the cost model expects to save. It is a guide for
	// ordering attempts, not a promise; the saving that counts is measured
	// after gopls has done the work.
	Predicted int
}

func (c Candidate) Range() string {
	return fmt.Sprintf("%s:%d:%d-%d:%d", filepath.Base(c.File), c.StartLine, c.StartCol, c.EndLine, c.EndCol)
}

// Available reports whether gopls can be found.
func Available() (string, bool) {
	if path, err := exec.LookPath("gopls"); err == nil {
		return path, true
	}
	home, err := os.UserHomeDir()
	if err == nil {
		candidate := filepath.Join(home, "go", "bin", "gopls")
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate, true
		}
	}
	return "", false
}

// Candidates finds blocks worth extracting from the functions a scoring pass
// found expensive, ordered by what the model expects each to save.
func Candidates(path string, w cost.Weights, minStatements int) ([]Candidate, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, err
	}
	scored, err := cost.ScoreFile(path, w)
	if err != nil {
		return nil, err
	}
	costOf := map[string]cost.Function{}
	for _, f := range scored {
		costOf[f.Name] = f
	}

	var out []Candidate
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		name := funcName(fn)
		if costOf[name].Total == 0 {
			continue // already within every allowance
		}
		for _, block := range nestedBlocks(fn.Body, w.MaxDepth) {
			if len(block.stmts) < minStatements {
				continue
			}
			start := fset.Position(block.stmts[0].Pos())
			end := fset.Position(block.stmts[len(block.stmts)-1].End())
			out = append(out, Candidate{
				File:       path,
				Function:   name,
				StartLine:  start.Line,
				StartCol:   start.Column,
				EndLine:    end.Line,
				EndCol:     end.Column,
				Statements: len(block.stmts),
				Depth:      block.depth,
				Predicted:  predict(costOf[name], block, w),
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Predicted > out[j].Predicted })
	return out, nil
}

// predict estimates the saving: the enclosing function loses these statements
// and the nesting they sat in, and a new function gains them at the top level.
// Parameters of the new function are unknown until gopls resolves the free
// variables, so this is deliberately an approximation.
func predict(f cost.Function, b block, w cost.Weights) int {
	if f.Statements == 0 {
		return 0
	}
	shorter := f
	shorter.Statements = f.Statements - len(b.stmts)
	shorter.DeepStmts = f.DeepStmts - b.buried
	if shorter.DeepStmts < 0 {
		shorter.DeepStmts = 0
	}
	return f.Total - cost.Recompute(shorter, w)
}

type block struct {
	stmts  []ast.Stmt
	depth  int
	buried int
}

// nestedBlocks collects the statement lists that sit below the allowed depth,
// which are the ones whose removal takes nesting with it.
func nestedBlocks(body *ast.BlockStmt, maxDepth int) []block {
	var out []block
	var visit func(stmts []ast.Stmt, depth int)
	visit = func(stmts []ast.Stmt, depth int) {
		if depth > maxDepth && len(stmts) > 0 && extractable(stmts) {
			buried := 0
			for range stmts {
				buried += depth - maxDepth
			}
			out = append(out, block{stmts: stmts, depth: depth, buried: buried})
		}
		for _, stmt := range stmts {
			switch s := stmt.(type) {
			case *ast.IfStmt:
				visit(s.Body.List, depth+1)
				if els, ok := s.Else.(*ast.BlockStmt); ok {
					visit(els.List, depth+1)
				}
			case *ast.ForStmt:
				visit(s.Body.List, depth+1)
			case *ast.RangeStmt:
				visit(s.Body.List, depth+1)
			case *ast.SwitchStmt:
				for _, c := range s.Body.List {
					if cc, ok := c.(*ast.CaseClause); ok {
						visit(cc.Body, depth+1)
					}
				}
			}
		}
	}
	visit(body.List, 1)
	return out
}

// extractable rejects blocks gopls cannot lift cleanly. Control flow that
// escapes the block - a return, break, continue or goto - has no meaning once
// the statements live in another function.
func extractable(stmts []ast.Stmt) bool {
	ok := true
	for _, stmt := range stmts {
		ast.Inspect(stmt, func(n ast.Node) bool {
			switch n.(type) {
			case *ast.ReturnStmt, *ast.BranchStmt:
				ok = false
			case *ast.FuncLit:
				return false // its returns belong to itself
			}
			return ok
		})
	}
	return ok
}

// Extract asks gopls to lift a candidate into its own function, writing the
// change to disk. The module directory must contain the file.
func Extract(goplsPath, moduleDir string, c Candidate) error {
	rel, err := filepath.Rel(moduleDir, c.File)
	if err != nil {
		rel = c.File
	}
	spec := fmt.Sprintf("%s:%d:%d-%d:%d", rel, c.StartLine, c.StartCol, c.EndLine, c.EndCol)
	cmd := exec.Command(goplsPath, "codeaction",
		"-kind=refactor.extract.function", "-exec", "-w", spec)
	cmd.Dir = moduleDir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gopls declined %s: %s", spec, firstLine(stderr.String()))
	}
	return nil
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

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	if s == "" {
		return "no reason given"
	}
	return s
}
