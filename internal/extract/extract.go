// Package extract moves a range of statements out of a Go function into a
// new function, growing or adapting the range until the move keeps the
// program's meaning.
//
// A range R depends on things outside it: variables it reads and writes,
// jumps to labels and loops, the frame its defers and returns belong to, local
// types. Each such edge either crosses into the new function through an
// adapter, or R grows to take the other end along (R = R ∪ deps(R), until
// closed). Adapters come first; growth is the fallback for edges no call can
// carry:
//
//	read variable                      parameter
//	written, read after (or next loop) result: x = f(x)
//	aliased (&x, closure, ptr method)  &x, uses become (*x)
//	declared in R, used after          var at the call site + result
//	  ...and aliased and written       declared at the call site, shared by &x
//	break/continue/goto/return out     a signal the call site acts on
//	defer, R not in tail position      registered with the caller's runner
//	local type                         moved to package level
//	enclosing type parameters          copied; f[T](...)
//	defer reaching recover             grow to the tail: recover needs the frame
//	fallthrough, goto into R           grow
//
// In tail position (nothing of the enclosing function runs after R) the call
// is "return f(...)" and defers and named results move along. All edits go
// through package edit; the result is gofmt'd.
package extract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/dhilst/sx/internal/edit"
	"strings"
)

type X struct {
	fset  *token.FileSet
	info  *types.Info
	pkg   *types.Package
	file  *ast.File
	src   []byte
	decl  *ast.FuncDecl  // top-level declaration holding R
	fn    ast.Node       // innermost function (FuncDecl or FuncLit) holding R
	body  *ast.BlockStmt // its body
	ftype *ast.FuncType  // its signature
	sig   *types.Signature
	hoist map[*types.TypeName]*ast.DeclStmt // local types moved to package level
	named map[string]bool                   // packages the new signature names
	esc   map[ast.Stmt]bool                 // escaping jumps/returns rewritten into signals
	pick  func(lo, hi token.Pos)            // choose the innermost function holding [lo, hi)
	opt   Options
	par   map[ast.Node]ast.Node
	lists map[ast.Node][]ast.Stmt // owner (BlockStmt/CaseClause/CommClause) -> list
	where map[ast.Stmt]struct {
		owner ast.Node
		idx   int
	}
	owner ast.Node // current selection: lists[owner][i:j]
	i, j  int
	log   []string
}

// failure is how a refusal travels from deep inside the analysis to Extract.
type failure string

func fail(format string, a ...any) { panic(failure(fmt.Sprintf(format, a...))) }

// ---------------------------------------------------------------------------
// loading

func load(dir, target string) *X {
	fset := token.NewFileSet()
	entries, _ := os.ReadDir(dir)
	var files []*ast.File
	var tf *ast.File
	var src []byte
	for _, e := range entries {
		n := e.Name()
		if !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		if ok, _ := build.Default.MatchFile(dir, n); !ok {
			continue // build constraints (GOOS, tags)
		}
		data, _ := os.ReadFile(filepath.Join(dir, n))
		f, err := parser.ParseFile(fset, filepath.Join(dir, n), data, parser.ParseComments)
		if err != nil {
			fail("%v", err)
		}
		files = append(files, f)
		if n == target {
			tf, src = f, data
		}
	}
	if tf == nil {
		fail("no %s in %s", target, dir)
	}
	exports := map[string]string{}
	cmd := exec.Command("go", "list", "-e", "-export", "-deps", "-json=ImportPath,Export", ".")
	cmd.Dir = dir
	out, _ := cmd.Output()
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var e struct{ ImportPath, Export string }
		if dec.Decode(&e) != nil {
			break
		}
		exports[e.ImportPath] = e.Export
	}
	imp := importer.ForCompiler(fset, "gc", func(p string) (io.ReadCloser, error) {
		if f, ok := exports[p]; ok && f != "" {
			return os.Open(f)
		}
		return nil, fmt.Errorf("no export data for %s", p)
	})
	info := &types.Info{
		Types: map[ast.Expr]types.TypeAndValue{}, Defs: map[*ast.Ident]types.Object{},
		Uses: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{},
		Scopes: map[ast.Node]*types.Scope{},
	}
	conf := types.Config{Importer: imp}
	pkg, err := conf.Check("main", fset, files, info)
	if err != nil {
		fail("type check: %v", err)
	}
	return &X{fset: fset, info: info, pkg: pkg, file: tf, src: src}
}

func (x *X) line(p token.Pos) int { return x.fset.Position(p).Line }
func (x *X) off(p token.Pos) int  { return x.fset.Position(p).Offset }

// ---------------------------------------------------------------------------
// indexing the enclosing function

func (x *X) index(decl *ast.FuncDecl, fn ast.Node) {
	x.decl, x.fn = decl, fn
	switch f := fn.(type) {
	case *ast.FuncDecl:
		x.body, x.ftype = f.Body, f.Type
		x.sig = x.info.Defs[f.Name].Type().(*types.Signature)
	case *ast.FuncLit:
		x.body, x.ftype = f.Body, f.Type
		x.sig = x.info.TypeOf(f).(*types.Signature)
	}
	x.par = map[ast.Node]ast.Node{}
	x.lists = map[ast.Node][]ast.Stmt{}
	x.where = map[ast.Stmt]struct {
		owner ast.Node
		idx   int
	}{}
	var stack []ast.Node
	ast.Inspect(decl, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return false
		}
		if len(stack) > 0 {
			x.par[n] = stack[len(stack)-1]
		}
		stack = append(stack, n)
		var list []ast.Stmt
		switch n := n.(type) {
		case *ast.BlockStmt:
			switch x.par[n].(type) {
			case *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
				return true // its "statements" are case clauses
			}
			list = n.List
		case *ast.CaseClause:
			list = n.Body
		case *ast.CommClause:
			list = n.Body
		default:
			return true
		}
		x.lists[n] = list
		for i, s := range list {
			x.where[s] = struct {
				owner ast.Node
				idx   int
			}{n, i}
		}
		return true
	})
}

func (x *X) sel() []ast.Stmt { return x.lists[x.owner][x.i:x.j] }
func (x *X) span() (token.Pos, token.Pos) {
	s := x.sel()
	return s[0].Pos(), s[len(s)-1].End()
}
func (x *X) inR(p token.Pos) bool { a, b := x.span(); return p >= a && p < b }
func (x *X) desc() string {
	a, b := x.span()
	return fmt.Sprintf("%d-%d", x.line(a), x.line(b))
}

// chain: (owner, idx) of every listed statement enclosing n, outermost first.
type step struct {
	owner ast.Node
	idx   int
}

func (x *X) chain(n ast.Node) []step {
	var out []step
	for m := n; m != nil && m != x.fn; m = x.par[m] {
		if s, ok := m.(ast.Stmt); ok {
			if w, ok := x.where[s]; ok {
				out = append([]step{{w.owner, w.idx}}, out...)
			}
		}
	}
	return out
}

// cover grows R to the smallest statement run that holds R and every node.
func (x *X) cover(why string, nodes ...ast.Node) bool {
	s := x.sel()
	all := append([]ast.Node{s[0], s[len(s)-1]}, nodes...)
	chains := make([][]step, len(all))
	for k, n := range all {
		chains[k] = x.chain(n)
	}
	depth := 0
	for {
		if depth >= len(chains[0]) {
			break
		}
		o := chains[0][depth].owner
		same := true
		for _, c := range chains[1:] {
			if depth >= len(c) || c[depth].owner != o {
				same = false
				break
			}
		}
		if !same {
			break
		}
		depth++
	}
	if depth == 0 {
		fail("cannot cover: %s", why)
	}
	owner := chains[0][depth-1].owner
	i, j := 1<<30, -1
	for _, c := range chains {
		i, j = min(i, c[depth-1].idx), max(j, c[depth-1].idx+1)
	}
	if owner == x.owner && i == x.i && j == x.j {
		return false
	}
	before := x.desc()
	x.owner, x.i, x.j = owner, i, j
	x.log = append(x.log, fmt.Sprintf("%-60s %s -> %s", why, before, x.desc()))
	return true
}

// ---------------------------------------------------------------------------
// control flow

func (x *X) isPanic(s ast.Stmt) bool {
	e, ok := s.(*ast.ExprStmt)
	if !ok {
		return false
	}
	c, ok := e.X.(*ast.CallExpr)
	if !ok {
		return false
	}
	id, ok := c.Fun.(*ast.Ident)
	return ok && id.Name == "panic" && x.info.Uses[id] == types.Universe.Lookup("panic")
}

func (x *X) hasBreak(target ast.Stmt, body ast.Node) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		if b, ok := n.(*ast.BranchStmt); ok && b.Tok == token.BREAK && x.branchTarget(b) == target {
			found = true
		}
		return !found
	})
	return found
}

// terminating approximates the spec's terminating statements.
func (x *X) terminating(s ast.Stmt) bool {
	switch s := s.(type) {
	case *ast.ReturnStmt:
		return true
	case *ast.BranchStmt:
		return s.Tok == token.GOTO || x.esc[s]
	case *ast.ExprStmt:
		return x.isPanic(s)
	case *ast.BlockStmt:
		return len(s.List) > 0 && x.terminating(s.List[len(s.List)-1])
	case *ast.IfStmt:
		return s.Else != nil && x.terminating(s.Body) && x.terminating(s.Else)
	case *ast.LabeledStmt:
		return x.terminating(s.Stmt)
	case *ast.ForStmt:
		return s.Cond == nil && !x.hasBreak(s, s.Body)
	case *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
		var body *ast.BlockStmt
		switch s := s.(type) {
		case *ast.SwitchStmt:
			body = s.Body
		case *ast.TypeSwitchStmt:
			body = s.Body
		case *ast.SelectStmt:
			body = s.Body
		}
		if x.hasBreak(s, body) {
			return false
		}
		hasDefault := false
		for _, c := range body.List {
			var list []ast.Stmt
			switch c := c.(type) {
			case *ast.CaseClause:
				hasDefault = hasDefault || c.List == nil
				list = c.Body
			case *ast.CommClause:
				hasDefault = true // select without default blocks forever: fine
				list = c.Body
			}
			if len(list) == 0 {
				return false
			}
			last := list[len(list)-1]
			if b, ok := last.(*ast.BranchStmt); ok && b.Tok == token.FALLTHROUGH {
				continue
			}
			if !x.terminating(last) {
				return false
			}
		}
		return hasDefault
	}
	return false
}

func (x *X) labeled(name string) *ast.LabeledStmt {
	var out *ast.LabeledStmt
	ast.Inspect(x.body, func(n ast.Node) bool {
		if l, ok := n.(*ast.LabeledStmt); ok && l.Label.Name == name {
			out = l
		}
		return out == nil
	})
	return out
}

// branchTarget: the statement a break/continue/goto/fallthrough refers to.
func (x *X) branchTarget(b *ast.BranchStmt) ast.Stmt {
	if b.Label != nil {
		l := x.labeled(b.Label.Name)
		if b.Tok == token.GOTO {
			return l
		}
		return l.Stmt
	}
	for m := x.par[b]; m != nil; m = x.par[m] {
		switch m.(type) {
		case *ast.FuncLit:
			return nil
		case *ast.ForStmt, *ast.RangeStmt:
			if b.Tok != token.FALLTHROUGH {
				return m.(ast.Stmt)
			}
		case *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
			if b.Tok == token.BREAK || b.Tok == token.FALLTHROUGH {
				return m.(ast.Stmt)
			}
		}
	}
	return nil
}

func (x *X) void() bool { return x.sig.Results().Len() == 0 }

// tail: after R nothing of the enclosing function runs but returning.
func (x *X) tail() bool {
	s := x.sel()
	if x.terminating(s[len(s)-1]) {
		return true
	}
	return x.void() && x.owner == ast.Node(x.body) && x.j == len(x.lists[x.owner])
}

// toTail extends R to the end of its list, lifting to the enclosing
// statement while control could still fall out of it.
func (x *X) toTail(why string) {
	for !x.tail() {
		list := x.lists[x.owner]
		if x.j < len(list) {
			x.cover(why+": to the end of the block", list[len(list)-1])
			continue
		}
		// the block can fall through to code after its statement: lift
		var up ast.Stmt
		for m := x.par[x.owner]; m != nil; m = x.par[m] {
			if s, ok := m.(ast.Stmt); ok {
				if _, listed := x.where[s]; listed {
					up = s
					break
				}
			}
		}
		if up == nil {
			fail("cannot reach a tail position")
		}
		x.cover(why+": lift to enclosing statement", up)
	}
}

// ---------------------------------------------------------------------------
// variables

// localVar: a variable (or const) of the enclosing function, not a field.
func (x *X) local(o types.Object) bool {
	switch o := o.(type) {
	case *types.Var:
		if o.IsField() {
			return false
		}
	case *types.Const:
	default:
		return false
	}
	return o.Pos() >= x.decl.Pos() && o.Pos() < x.decl.End()
}

// root of an addressable expression: x in x, x.f, x[i] (arrays), (x)
func (x *X) root(e ast.Expr) *ast.Ident {
	for {
		switch t := e.(type) {
		case *ast.Ident:
			return t
		case *ast.ParenExpr:
			e = t.X
		case *ast.SelectorExpr:
			if _, ptr := x.info.TypeOf(t.X).Underlying().(*types.Pointer); ptr {
				return nil
			}
			if x.info.Selections[t] == nil {
				return nil // package-qualified
			}
			e = t.X
		case *ast.IndexExpr:
			if _, arr := x.info.TypeOf(t.X).Underlying().(*types.Array); !arr {
				return nil // slice/map/pointer: shared, not a write of the variable
			}
			e = t.X
		default:
			return nil
		}
	}
}

type facts struct {
	uses     map[types.Object][]*ast.Ident // outer objects used in R
	order    []types.Object
	written  map[types.Object]bool
	aliased  map[types.Object]string
	declared map[types.Object]*ast.Ident // declared in R
	defer_   bool
	return_  bool
	bare     bool
	escapes  []ast.Stmt // break/continue/goto leaving R, and returns (not in closures)
	defers   []*ast.DeferStmt
}

func (x *X) facts() facts {
	f := facts{uses: map[types.Object][]*ast.Ident{}, written: map[types.Object]bool{},
		aliased: map[types.Object]string{}, declared: map[types.Object]*ast.Ident{}}
	a, b := x.span()
	outer := func(o types.Object) bool { return o != nil && x.local(o) && !(o.Pos() >= a && o.Pos() < b) }
	var lits []ast.Node
	inLit := func(n ast.Node) bool {
		for _, l := range lits {
			if n.Pos() >= l.Pos() && n.End() <= l.End() {
				return true
			}
		}
		return false
	}
	for _, s := range x.sel() {
		ast.Inspect(s, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.FuncLit:
				lits = append(lits, n)
			case *ast.DeferStmt:
				if !inLit(n) {
					f.defer_ = true
					f.defers = append(f.defers, n)
				}
			case *ast.ReturnStmt:
				if !inLit(n) {
					f.return_ = true
					f.bare = f.bare || len(n.Results) == 0
					f.escapes = append(f.escapes, n)
				}
			case *ast.BranchStmt:
				if !inLit(n) && n.Tok != token.FALLTHROUGH {
					if t := x.branchTarget(n); t != nil && !(t.Pos() >= a && t.End() <= b) {
						f.escapes = append(f.escapes, n)
					}
				}
			case *ast.Ident:
				if o := x.info.Defs[n]; o != nil && x.local(o) {
					f.declared[o] = n
				}
				if o := x.info.Uses[n]; outer(o) {
					if _, seen := f.uses[o]; !seen {
						f.order = append(f.order, o)
					}
					f.uses[o] = append(f.uses[o], n)
					if inLit(n) {
						f.aliased[o] = "captured by a closure"
					}
				}
			case *ast.AssignStmt:
				for _, l := range n.Lhs {
					if r := x.root(l); r != nil && outer(x.info.Uses[r]) {
						f.written[x.info.Uses[r]] = true
					}
				}
			case *ast.IncDecStmt:
				if r := x.root(n.X); r != nil && outer(x.info.Uses[r]) {
					f.written[x.info.Uses[r]] = true
				}
			case *ast.RangeStmt:
				for _, e := range []ast.Expr{n.Key, n.Value} {
					if e != nil && n.Tok == token.ASSIGN {
						if r := x.root(e); r != nil && outer(x.info.Uses[r]) {
							f.written[x.info.Uses[r]] = true
						}
					}
				}
			case *ast.UnaryExpr:
				if n.Op == token.AND {
					if r := x.root(n.X); r != nil && outer(x.info.Uses[r]) {
						f.aliased[x.info.Uses[r]] = "address taken"
					}
				}
			case *ast.SelectorExpr:
				// v.M() with a pointer receiver takes &v implicitly
				if s := x.info.Selections[n]; s != nil && s.Kind() == types.MethodVal {
					if sig := s.Obj().Type().(*types.Signature); sig.Recv() != nil {
						if _, ptr := sig.Recv().Type().(*types.Pointer); ptr {
							if _, isPtr := x.info.TypeOf(n.X).Underlying().(*types.Pointer); !isPtr {
								if r := x.root(n.X); r != nil && outer(x.info.Uses[r]) {
									f.aliased[x.info.Uses[r]] = "pointer-receiver method call"
								}
							}
						}
					}
				}
			}
			return true
		})
	}
	// a bare return reads the enclosing function's named results
	if f.bare {
		r := x.sig.Results()
		for k := 0; k < r.Len(); k++ {
			if o := r.At(k); o.Name() != "" && outer(o) {
				if _, seen := f.uses[o]; !seen {
					f.order = append(f.order, o)
					f.uses[o] = nil
				}
			}
		}
	}
	return f
}

// adaptDefers: a defer in R is registered with the caller (a cleanup list the
// caller's own deferred runner drains) instead of growing R to the tail.
func (x *X) adaptDefers() bool { return !x.opt.GrowDefers }

// defersNeedFrame names a deferred call that calls recover: recover only
// stops a panic when called by a function the panicking frame deferred, so
// such a call cannot be run from the caller's runner.
func (x *X) defersNeedFrame(ds []*ast.DeferStmt) string {
	callsRecover := func(n ast.Node) bool {
		found := false
		ast.Inspect(n, func(m ast.Node) bool {
			if c, ok := m.(*ast.CallExpr); ok {
				if id, ok := c.Fun.(*ast.Ident); ok && id.Name == "recover" && x.info.Uses[id] == types.Universe.Lookup("recover") {
					found = true
				}
			}
			return !found
		})
		return found
	}
	bodies := map[types.Object]*ast.BlockStmt{}
	for _, d := range x.file.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Body != nil {
			bodies[x.info.Defs[fd.Name]] = fd.Body
		}
	}
	for _, d := range ds {
		switch fun := d.Call.Fun.(type) {
		case *ast.FuncLit:
			if callsRecover(fun.Body) {
				return fmt.Sprintf("the deferred closure at line %d calls recover", x.line(d.Pos()))
			}
		case *ast.Ident, *ast.SelectorExpr:
			var id *ast.Ident
			if i, ok := fun.(*ast.Ident); ok {
				id = i
			} else {
				id = fun.(*ast.SelectorExpr).Sel
			}
			if body := bodies[x.info.Uses[id]]; body != nil && callsRecover(body) {
				return fmt.Sprintf("deferred %s (line %d) calls recover", id.Name, x.line(d.Pos()))
			}
		}
	}
	return ""
}

// deferAdapter rewrites every defer in R into an append to the caller's
// cleanup list; function value and arguments are evaluated where the defer
// was, as Go does.
func (x *X) deferAdapter(ds []*ast.DeferStmt, body *edit.Buffer, list string) {
	for _, d := range ds {
		c := d.Call
		render := func(n ast.Node) string {
			t, err := body.Render(body.NodeSpan(n))
			if err != nil {
				fail("%v", err)
			}
			return t
		}
		// Each argument is evaluated now into a variable of the parameter's
		// type (so untyped constants and nil convert as in the original call).
		var decls, callArgs []string
		_, isLit := c.Fun.(*ast.FuncLit)
		_, isBuiltin := x.info.Uses[identOf(c.Fun)].(*types.Builtin)
		var sig *types.Signature
		if !isBuiltin {
			sig = x.info.TypeOf(c.Fun).Underlying().(*types.Signature)
		}
		if len(c.Args) == 1 {
			if tup, ok := x.info.TypeOf(c.Args[0]).(*types.Tuple); ok { // f(g()) with g multi-valued
				var vs []string
				for k := 0; k < tup.Len(); k++ {
					vs = append(vs, fmt.Sprintf("a%d", k))
				}
				decls = append(decls, strings.Join(vs, ", ")+" := "+render(c.Args[0]))
				callArgs = vs
			}
		}
		if callArgs == nil {
			for k, a := range c.Args {
				v := fmt.Sprintf("a%d", k)
				var t types.Type
				switch {
				case isBuiltin:
					t = types.Default(x.info.TypeOf(a))
					if b, ok := t.(*types.Basic); ok && b.Kind() == types.UntypedNil {
						t = types.Universe.Lookup("any").Type()
					}
				case sig.Variadic() && k >= sig.Params().Len()-1:
					t = sig.Params().At(sig.Params().Len() - 1).Type()
					if !c.Ellipsis.IsValid() {
						t = t.(*types.Slice).Elem()
					}
				default:
					t = sig.Params().At(k).Type()
				}
				decls = append(decls, fmt.Sprintf("var %s %s = %s", v, x.typeStr(t), render(a)))
				callArgs = append(callArgs, v)
			}
			if c.Ellipsis.IsValid() {
				callArgs[len(callArgs)-1] += "..."
			}
		}
		var wrap string
		switch {
		case isLit && len(c.Args) == 0:
			wrap = render(c.Fun)
		case isBuiltin:
			wrap = fmt.Sprintf("func() func() {\n%s\nreturn func() { %s(%s) }\n}()", strings.Join(decls, "\n"), render(c.Fun), strings.Join(callArgs, ", "))
		default:
			wrap = fmt.Sprintf("func() func() {\nf_ := %s\n%s\nreturn func() { f_(%s) }\n}()", render(c.Fun), strings.Join(decls, "\n"), strings.Join(callArgs, ", "))
		}
		body.ReplaceRendered(body.NodeSpan(d), fmt.Sprintf("*%s = append(*%s, %s)", list, list, wrap), "defer adapter")
		x.log = append(x.log, fmt.Sprintf("%-60s", fmt.Sprintf("defer at line %d registered with the caller", x.line(d.Pos()))))
	}
}

// isCommentOnly: lines [A, B] hold nothing but comments, blanks and braces.
func isCommentOnly(x *X, A, B int) bool {
	lines := strings.Split(string(x.src), "\n")
	for l := A; l <= B && l <= len(lines); l++ {
		t := strings.TrimSpace(lines[l-1])
		if t != "" && !strings.HasPrefix(t, "//") && strings.Trim(t, "{}()") != "" {
			return false
		}
	}
	return true
}

func isTypeParamObj(o *types.TypeName) bool { _, ok := o.Type().(*types.TypeParam); return ok }

func identOf(e ast.Expr) *ast.Ident {
	switch e := e.(type) {
	case *ast.Ident:
		return e
	case *ast.ParenExpr:
		return identOf(e.X)
	}
	return nil
}

// signals: jumps and returns leaving R become a code the call site acts on,
// instead of growing R to hold their targets.
func (x *X) signals() bool { return !x.opt.GrowJumps }

type signalPlan struct {
	types, decls, lhs, final, after []string
}

// signalPlan rewrites every escape in R into `return <outs>, <rets>, k` and
// returns what the call site needs: the extra result types, declarations,
// assignment targets, the values for the normal exit, and one
// `if ctl == k { <the original jump> }` per distinct escape.
func (x *X) signalPlan(f facts, body *edit.Buffer, exprOf map[types.Object]string, declared []types.Object) *signalPlan {
	if len(f.escapes) == 0 || !x.signals() {
		return nil
	}
	x.esc = map[ast.Stmt]bool{}
	for _, e := range f.escapes {
		x.esc[e] = true
	}
	names := map[string]bool{}
	ast.Inspect(x.decl, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			names[id.Name] = true
		}
		return true
	})
	fresh := func(base string) string {
		n := base
		for names[n] {
			n += "_"
		}
		names[n] = true
		return n
	}
	zero := func(t types.Type) string { return "*new(" + x.typeStr(t) + ")" }
	// outs: written outer vars (by value) and vars declared in R and used after
	var outObjs []types.Object
	for _, o := range f.order {
		if ptr, _ := x.byPointer(o); !ptr && f.written[o] && (x.usedAfterOrAround(o) || x.carried(o)) {
			outObjs = append(outObjs, o)
		}
	}
	outObjs = append(outObjs, declared...)
	outsAt := func(p token.Pos) []string {
		var v []string
		for _, o := range outObjs {
			if o.Pos() >= p && x.inR(o.Pos()) {
				v = append(v, zero(o.Type())) // not declared yet at this escape
			} else {
				v = append(v, exprOf[o])
			}
		}
		return v
	}
	res := x.sig.Results()
	hasRet := false
	for _, e := range f.escapes {
		if _, ok := e.(*ast.ReturnStmt); ok {
			hasRet = true
		}
	}
	p := &signalPlan{}
	var retNames, retZeros []string
	if hasRet {
		for k := 0; k < res.Len(); k++ {
			n := fresh(fmt.Sprintf("ret%d", k))
			retNames = append(retNames, n)
			retZeros = append(retZeros, zero(res.At(k).Type()))
			p.types = append(p.types, x.typeStr(res.At(k).Type()))
			p.decls = append(p.decls, "var "+n+" "+x.typeStr(res.At(k).Type()))
			p.lhs = append(p.lhs, n)
		}
	}
	ctl := fresh("ctl")
	p.types = append(p.types, "int")
	p.decls = append(p.decls, "var "+ctl+" int")
	p.lhs = append(p.lhs, ctl)
	codes := map[string]int{}
	for _, e := range f.escapes {
		var key, jump string
		switch e := e.(type) {
		case *ast.BranchStmt:
			key = e.Tok.String()
			if e.Label != nil {
				key += " " + e.Label.Name
			}
			jump = key
		case *ast.ReturnStmt:
			key = "return"
			jump = strings.TrimSpace("return " + strings.Join(retNames, ", "))
		}
		code, ok := codes[key]
		if !ok {
			code = len(codes) + 1
			codes[key] = code
			p.after = append(p.after, fmt.Sprintf("if %s == %d {\n%s\n}", ctl, code, jump))
			x.log = append(x.log, fmt.Sprintf("%-60s", fmt.Sprintf("signal %d: %s leaves R (line %d)", code, key, x.line(e.Pos()))))
		}
		outs := outsAt(e.Pos())
		switch e := e.(type) {
		case *ast.BranchStmt:
			vals := append(append(outs, retZeros...), strconv.Itoa(code))
			body.ReplaceNode(e, "return "+strings.Join(vals, ", "), "signal "+key)
		case *ast.ReturnStmt:
			switch {
			case len(e.Results) == 0 && res.Len() > 0: // bare return: the named results
				var named []string
				for k := 0; k < res.Len(); k++ {
					named = append(named, exprOf[res.At(k)])
				}
				body.ReplaceNode(e, "return "+strings.Join(append(append(outs, named...), strconv.Itoa(code)), ", "), "signal return")
			case len(e.Results) == 0:
				body.ReplaceNode(e, "return "+strings.Join(append(outs, strconv.Itoa(code)), ", "), "signal return")
			case len(e.Results) == 1 && res.Len() > 1: // return g() with g multi-valued: split it
				text, err := body.Render(body.NodeSpan(e.Results[0]))
				if err != nil {
					fail("%v", err)
				}
				var tmps []string
				for k := 0; k < res.Len(); k++ {
					tmps = append(tmps, fmt.Sprintf("r%d_", k))
				}
				repl := fmt.Sprintf("{\n%s := %s\nreturn %s\n}", strings.Join(tmps, ", "), text,
					strings.Join(append(append(outs, tmps...), strconv.Itoa(code)), ", "))
				body.ReplaceRendered(body.NodeSpan(e), repl, "signal return (split)")
			default:
				if len(outs) > 0 {
					body.Insert(x.off(e.Results[0].Pos()), strings.Join(outs, ", ")+", ", "signal return: outs")
				}
				body.Insert(x.off(e.End()), ", "+strconv.Itoa(code), "signal return: code")
			}
		}
	}
	p.final = append(retZeros, "0")
	return p
}

// usesOutside: every identifier referring to o outside R (declaration included).
func (x *X) usesOutside(o types.Object) []ast.Node {
	var out []ast.Node
	ast.Inspect(x.decl, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && !x.inR(id.Pos()) && (x.info.Uses[id] == o || x.info.Defs[id] == o) {
			out = append(out, id)
		}
		return true
	})
	return out
}

// mutated: o is assigned, incremented, address-taken or has a pointer
// method called on it anywhere in the function (its declaration excluded).
func (x *X) mutated(o types.Object) bool {
	hit := false
	is := func(e ast.Expr) {
		if r := x.root(e); r != nil && x.info.Uses[r] == o {
			hit = true
		}
	}
	ast.Inspect(x.decl.Body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			for _, l := range n.Lhs {
				is(l)
			}
		case *ast.IncDecStmt:
			is(n.X)
		case *ast.RangeStmt:
			if n.Tok == token.ASSIGN {
				for _, e := range []ast.Expr{n.Key, n.Value} {
					if e != nil {
						is(e)
					}
				}
			}
		case *ast.UnaryExpr:
			if n.Op == token.AND {
				is(n.X)
			}
		case *ast.SelectorExpr:
			if s := x.info.Selections[n]; s != nil && s.Kind() == types.MethodVal {
				if _, ptr := s.Obj().Type().(*types.Signature).Recv().Type().(*types.Pointer); ptr {
					is(n.X)
				}
			}
		}
		return !hit
	})
	return hit
}

// aliased: somewhere in the declaration (inside R or not) o's address is
// taken, a pointer method is called on it, or a closure captures it. Then a
// copy of o is a different variable from o, observably.
func (x *X) aliased(o types.Object) string {
	why := ""
	is := func(e ast.Expr, w string) {
		if r := x.root(e); r != nil && x.info.Uses[r] == o && why == "" {
			why = w
		}
	}
	var lits []*ast.FuncLit
	ast.Inspect(x.decl.Body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			lits = append(lits, n)
		case *ast.UnaryExpr:
			if n.Op == token.AND {
				is(n.X, fmt.Sprintf("address taken (line %d)", x.line(n.Pos())))
			}
		case *ast.SelectorExpr:
			if s := x.info.Selections[n]; s != nil && s.Kind() == types.MethodVal {
				if _, ptr := s.Obj().Type().(*types.Signature).Recv().Type().(*types.Pointer); ptr {
					if _, isPtr := x.info.TypeOf(n.X).Underlying().(*types.Pointer); !isPtr {
						is(n.X, fmt.Sprintf("pointer method %s (line %d)", n.Sel.Name, x.line(n.Pos())))
					}
				}
			}
		case *ast.Ident:
			if x.info.Uses[n] == o && why == "" {
				for _, l := range lits { // a use inside a closure that o was declared outside of
					if n.Pos() >= l.Pos() && n.End() <= l.End() && !(o.Pos() >= l.Pos() && o.Pos() < l.End()) {
						why = fmt.Sprintf("captured by a closure (line %d)", x.line(l.Pos()))
					}
				}
			}
		}
		return true
	})
	return why
}

// byPointer: o must cross into the new function as &o, because a copy would
// be observably different (aliased and mutated).
func (x *X) byPointer(o types.Object) (bool, string) {
	if _, ok := o.(*types.Var); !ok {
		return false, ""
	}
	if why := x.aliased(o); why != "" && x.mutated(o) {
		return true, why
	}
	return false, ""
}

// carried: R sits in a loop that does not contain o's declaration, so a
// value R writes to o is read again by the next iteration.
func (x *X) carried(o types.Object) bool {
	for m := x.par[x.owner]; m != nil && m != x.fn; m = x.par[m] {
		switch m.(type) {
		case *ast.ForStmt, *ast.RangeStmt:
			if !(o.Pos() >= m.Pos() && o.Pos() < m.End()) {
				return true
			}
		case *ast.FuncLit:
			return false
		}
	}
	return false
}

func (x *X) isNamedResult(o types.Object) bool {
	r := x.sig.Results()
	for k := 0; k < r.Len(); k++ {
		if r.At(k) == o && o.Name() != "" {
			return true
		}
	}
	return false
}

func (x *X) usedAfterOrAround(o types.Object) bool {
	for _, n := range x.usesOutside(o) {
		if x.info.Defs[n.(*ast.Ident)] != o {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// the closure loop

func (x *X) grow() bool {
	a, b := x.span()
	for _, s := range x.sel() {
		var grew bool
		ast.Inspect(s, func(n ast.Node) bool {
			if grew {
				return false
			}
			switch n := n.(type) {
			case *ast.FuncLit:
				return false
			case *ast.BranchStmt:
				t := x.branchTarget(n)
				if t == nil {
					return true
				}
				if n.Tok == token.FALLTHROUGH {
					grew = x.cover(fmt.Sprintf("fallthrough (line %d) needs its switch", x.line(n.Pos())), t)
				} else if !(t.Pos() >= a && t.End() <= b) && !x.signals() {
					grew = x.cover(fmt.Sprintf("%s (line %d) targets line %d outside R", n.Tok, x.line(n.Pos()), x.line(t.Pos())), t)
				}
			case *ast.LabeledStmt:
				ast.Inspect(x.body, func(m ast.Node) bool {
					if g, ok := m.(*ast.BranchStmt); ok && g.Tok == token.GOTO && g.Label.Name == n.Label.Name && !x.inR(g.Pos()) {
						grew = grew || x.cover(fmt.Sprintf("goto %s (line %d) jumps into R", g.Label.Name, x.line(g.Pos())), g)
					}
					return true
				})
			}
			return true
		})
		if grew {
			return true
		}
	}
	f := x.facts()
	deferGrows := f.defer_ && (!x.adaptDefers() || x.defersNeedFrame(f.defers) != "")
	if deferGrows && x.adaptDefers() {
		x.log = append(x.log, fmt.Sprintf("%-60s", "defer adapter unusable: "+x.defersNeedFrame(f.defers)))
	}
	if (deferGrows || f.return_ && !x.signals()) && !x.tail() {
		what := "return"
		if f.defer_ {
			what = "defer"
		}
		before := len(x.log)
		x.toTail(what + " in R, R not in tail position")
		return len(x.log) > before
	}
	for _, o := range f.order {
		if x.tail() && x.isNamedResult(o) {
			continue // named results move into the new function with R
		}
		// (an aliased o redeclared by := in R is handled by rewriteDefines)
	}
	return false
}

// rewriteDefines turns `a, b := e` in R into assignments when one of the
// names now lives behind a pointer (an aliased outer variable reused by :=,
// or a shared declaration): `var a A` for the names still new here, then
// `a, (*b) = e`. Likewise `var b T = e` becomes `(*b) = e` (or a zero value).
func (x *X) rewriteDefines(body *edit.Buffer, exprOf map[types.Object]string, shared map[types.Object]bool) {
	isPtr := func(o types.Object) bool { return o != nil && strings.HasPrefix(exprOf[o], "(*") }
	for _, st := range x.sel() {
		ast.Inspect(st, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.AssignStmt:
				if n.Tok != token.DEFINE {
					return true
				}
				need := false
				for _, l := range n.Lhs {
					if id, ok := l.(*ast.Ident); ok && (isPtr(x.info.Uses[id]) || shared[x.info.Defs[id]]) {
						need = true
					}
				}
				if !need {
					return true
				}
				if _, inList := x.where[n]; !inList {
					fail("line %d: a := in a statement header reuses a shared variable", x.line(n.Pos()))
				}
				var decls, lhs []string
				for _, l := range n.Lhs {
					id := l.(*ast.Ident)
					switch {
					case id.Name == "_":
						lhs = append(lhs, "_")
					case x.info.Defs[id] != nil && shared[x.info.Defs[id]]:
						lhs = append(lhs, exprOf[x.info.Defs[id]])
					case x.info.Defs[id] != nil: // still declared here
						decls = append(decls, "var "+id.Name+" "+x.typeStr(x.info.Defs[id].Type()))
						lhs = append(lhs, id.Name)
					default: // reused variable
						if e, ok := exprOf[x.info.Uses[id]]; ok {
							lhs = append(lhs, e)
						} else {
							lhs = append(lhs, id.Name)
						}
					}
				}
				rhs, err := body.Render(edit.Span{Start: x.off(n.Rhs[0].Pos()), End: x.off(n.Rhs[len(n.Rhs)-1].End())})
				if err != nil {
					fail("%v", err)
				}
				body.ReplaceRendered(body.NodeSpan(n), strings.Join(append(decls, strings.Join(lhs, ", ")+" = "+rhs), "\n"), "rewrite :=")
				x.log = append(x.log, fmt.Sprintf("%-60s", fmt.Sprintf("line %d: := becomes = (a name is shared by pointer)", x.line(n.Pos()))))
			case *ast.DeclStmt:
				g, ok := n.Decl.(*ast.GenDecl)
				if !ok || g.Tok != token.VAR {
					return true
				}
				// `var a, b T = e1, e2` with b shared: `var a T` then `a, (*b) = e1, e2`
				// (a spec without values re-zeroes b, as the declaration did)
				any_ := false
				for _, sp := range g.Specs {
					for _, nm := range sp.(*ast.ValueSpec).Names {
						any_ = any_ || shared[x.info.Defs[nm]]
					}
				}
				if !any_ {
					return true
				}
				var lines []string
				for _, sp := range g.Specs {
					vs := sp.(*ast.ValueSpec)
					var lhs []string
					for _, nm := range vs.Names {
						o := x.info.Defs[nm]
						if shared[o] {
							lhs = append(lhs, exprOf[o])
						} else if nm.Name == "_" {
							lhs = append(lhs, "_")
						} else {
							lines = append(lines, "var "+nm.Name+" "+x.typeStr(o.Type()))
							lhs = append(lhs, nm.Name)
						}
					}
					if len(vs.Values) > 0 {
						v, err := body.Render(edit.Span{Start: x.off(vs.Values[0].Pos()), End: x.off(vs.Values[len(vs.Values)-1].End())})
						if err != nil {
							fail("%v", err)
						}
						lines = append(lines, strings.Join(lhs, ", ")+" = "+v)
					} else {
						for k, nm := range vs.Names {
							if o := x.info.Defs[nm]; shared[o] {
								lines = append(lines, lhs[k]+" = *new("+x.typeStr(o.Type())+")")
							}
						}
					}
				}
				body.ReplaceRendered(body.NodeSpan(n), strings.Join(lines, "\n"), "rewrite var")
				return true
			}
			return true
		})
	}
}

// redeclaredIn: o appears on the left of a := in R (reused, not declared).
func (x *X) redeclaredIn(o types.Object) bool {
	hit := false
	for _, s := range x.sel() {
		ast.Inspect(s, func(n ast.Node) bool {
			if a, ok := n.(*ast.AssignStmt); ok && a.Tok == token.DEFINE {
				for _, l := range a.Lhs {
					if id, ok := l.(*ast.Ident); ok && x.info.Uses[id] == o {
						hit = true
					}
				}
			}
			return !hit
		})
	}
	return hit
}

// localTypes: types declared inside the declaration that R uses, or that R
// declares and code outside R uses. Both are moved to package level, with
// the local types they mention. Types that mention local variables,
// constants or type parameters cannot move.
func (x *X) localTypes() {
	x.hoist = map[*types.TypeName]*ast.DeclStmt{}
	declOf := map[*types.TypeName]*ast.DeclStmt{}
	ast.Inspect(x.decl.Body, func(n ast.Node) bool {
		if d, ok := n.(*ast.DeclStmt); ok {
			if g := d.Decl.(*ast.GenDecl); g.Tok == token.TYPE {
				for _, sp := range g.Specs {
					declOf[x.info.Defs[sp.(*ast.TypeSpec).Name].(*types.TypeName)] = d
				}
			}
		}
		return true
	})
	var need func(t *types.TypeName, why string)
	need = func(t *types.TypeName, why string) {
		d := declOf[t]
		if d == nil || x.hoist[t] != nil {
			return
		}
		x.hoist[t] = d
		x.log = append(x.log, fmt.Sprintf("%-60s", fmt.Sprintf("move local type %s to package level (%s)", t.Name(), why)))
		ast.Inspect(d, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok {
				return true
			}
			switch o := x.info.Uses[id].(type) {
			case *types.TypeName:
				if _, tp := o.Type().(*types.TypeParam); tp {
					fail("local type %s mentions type parameter %s", t.Name(), o.Name())
				}
				need(o, "used by "+t.Name())
			case *types.Var, *types.Const:
				if x.local(o) {
					fail("local type %s mentions local %s", t.Name(), o.Name())
				}
			}
			return true
		})
	}
	// types the signature will mention: those of outer values used in R and
	// of values declared in R and used after it
	var walk func(t types.Type, seen map[types.Type]bool)
	walk = func(t types.Type, seen map[types.Type]bool) {
		if seen[t] {
			return
		}
		seen[t] = true
		switch t := t.(type) {
		case *types.Named:
			if tn := t.Obj(); declOf[tn] != nil {
				need(tn, "a value crossing the boundary has this type")
			}
			for k := 0; k < t.TypeArgs().Len(); k++ {
				walk(t.TypeArgs().At(k), seen)
			}
		case *types.Pointer:
			walk(t.Elem(), seen)
		case *types.Slice:
			walk(t.Elem(), seen)
		case *types.Array:
			walk(t.Elem(), seen)
		case *types.Map:
			walk(t.Key(), seen)
			walk(t.Elem(), seen)
		case *types.Chan:
			walk(t.Elem(), seen)
		case *types.Signature:
			for _, tup := range []*types.Tuple{t.Params(), t.Results()} {
				for k := 0; k < tup.Len(); k++ {
					walk(tup.At(k).Type(), seen)
				}
			}
		case *types.Struct:
			for k := 0; k < t.NumFields(); k++ {
				walk(t.Field(k).Type(), seen)
			}
		}
	}
	f := x.facts()
	for _, o := range f.order {
		walk(o.Type(), map[types.Type]bool{})
	}
	for o := range f.declared {
		if _, isVar := o.(*types.Var); isVar && x.usedAfterOrAround(o) {
			walk(o.Type(), map[types.Type]bool{})
		}
	}
	a, b := x.span()
	for t, d := range declOf {
		inside := d.Pos() >= a && d.End() <= b
		if !inside {
			for _, s := range x.sel() {
				ast.Inspect(s, func(n ast.Node) bool {
					if id, ok := n.(*ast.Ident); ok && x.info.Uses[id] == t {
						need(t, "R uses it, declared outside R")
					}
					return true
				})
			}
		} else if x.usedAfterOrAround(t) {
			need(t, "declared in R, used outside R")
		}
	}
}

// ---------------------------------------------------------------------------
// generation

func (x *X) typeStr(t types.Type) string {
	if n, ok := t.(*types.Named); ok && n.Obj().Pkg() != nil && n.Obj().Parent() != n.Obj().Pkg().Scope() && x.hoist[n.Obj()] == nil && !isTypeParamObj(n.Obj()) {
		fail("the signature needs local type %s", n.Obj().Name())
	}
	return types.TypeString(types.Default(t), func(p *types.Package) string {
		if p == x.pkg {
			return ""
		}
		if x.named == nil {
			x.named = map[string]bool{}
		}
		x.named[p.Path()] = true
		return p.Name()
	})
}

func (x *X) generate(name string) []byte {
	f := x.facts()
	a, b := x.span()
	tail := x.tail()
	x.localTypes()

	file := edit.NewBuffer(x.fset, x.src)
	body := file.Sub(edit.Span{Start: x.off(a), End: x.off(b)}) // R, edited before it moves
	route := func(p token.Pos) *edit.Buffer {
		if x.inR(p) {
			return body
		}
		return file
	}

	namedRes := tail && !x.void() && x.sig.Results().At(0).Name() != ""
	var params, args, prologue []string
	exprOf := map[types.Object]string{}
	shared := map[types.Object]bool{}
	taken := map[string]bool{}
	for d := range f.declared {
		taken[d.Name()] = true
	}
	if namedRes { // the new function declares the same named results
		for k := 0; k < x.sig.Results().Len(); k++ {
			taken[x.sig.Results().At(k).Name()] = true
		}
	}
	for _, o := range f.order {
		if namedRes && x.isNamedResult(o) {
			continue // handled by the prologue
		}
		pn := o.Name()
		for taken[pn] {
			pn += "_"
		}
		taken[pn] = true
		if ptr, why := x.byPointer(o); ptr {
			body.WrapIdents(f.uses[o], "(*%s)", pn, "deref "+o.Name())
			exprOf[o] = "(*" + pn + ")"
			params = append(params, pn+" *"+x.typeStr(o.Type()))
			args = append(args, "&"+o.Name())
			x.log = append(x.log, fmt.Sprintf("%-60s", fmt.Sprintf("pass %s by pointer: %s + mutated", o.Name(), why)))
			continue
		}
		if pn != o.Name() {
			body.RenameIdents(f.uses[o], pn, "rename "+o.Name())
			x.log = append(x.log, fmt.Sprintf("%-60s", fmt.Sprintf("rename param %s -> %s (R declares/uses another %s)", o.Name(), pn, o.Name())))
		}
		params = append(params, pn+" "+x.typeStr(o.Type()))
		args = append(args, o.Name())
		exprOf[o] = pn
	}
	var results, resultNames, resultDecls, lhs, finalExtra, afterCall, beforeCall []string
	if len(f.defers) > 0 && !tail {
		names := map[string]bool{}
		ast.Inspect(x.decl, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				names[id.Name] = true
			}
			return true
		})
		list, param := "cleanups", "defers"
		for names[list] {
			list += "_"
		}
		for names[param] || taken[param] {
			param += "_"
		}
		x.deferAdapter(f.defers, body, param)
		params = append(params, param+" *[]func()")
		args = append(args, "&"+list)
		beforeCall = append(beforeCall, "var "+list+" []func()",
			"defer func() {\nfor _, c := range "+list+" {\ndefer c()\n}\n}()")
	}
	if namedRes {
		var rs, ins []string
		for k := 0; k < x.sig.Results().Len(); k++ {
			r := x.sig.Results().At(k)
			rs = append(rs, r.Name()+" "+x.typeStr(r.Type()))
			params = append(params, r.Name()+"_in "+x.typeStr(r.Type()))
			args = append(args, r.Name())
			ins = append(ins, r.Name()+"_in")
			resultNames = append(resultNames, r.Name())
		}
		results = []string{"(" + strings.Join(rs, ", ") + ")"}
		prologue = append(prologue, strings.Join(resultNames, ", ")+" = "+strings.Join(ins, ", "))
	} else if tail && !x.void() {
		var rs []string
		for k := 0; k < x.sig.Results().Len(); k++ {
			rs = append(rs, x.typeStr(x.sig.Results().At(k).Type()))
		}
		results = []string{"(" + strings.Join(rs, ", ") + ")"}
	} else if !tail {
		var rs []string
		for _, o := range f.order { // written in R, read elsewhere: in and out (unless shared by pointer)
			if ptr, _ := x.byPointer(o); !ptr && f.written[o] && (x.usedAfterOrAround(o) || x.carried(o)) {
				rs = append(rs, x.typeStr(o.Type()))
				resultNames = append(resultNames, exprOf[o])
				lhs = append(lhs, o.Name())
			}
		}
		decl := []types.Object{}
		for o := range f.declared {
			if _, isVar := o.(*types.Var); isVar && x.usedAfterOrAround(o) {
				decl = append(decl, o)
			}
		}
		sort.Slice(decl, func(i, j int) bool { return decl[i].Pos() < decl[j].Pos() })
		// declared in R, aliased and written again after it: one variable must
		// serve both sides, so it is declared at the call site and shared
		var kept []types.Object
		for _, o := range decl {
			ptr, why := x.byPointer(o)
			if !ptr {
				kept = append(kept, o)
				exprOf[o] = o.Name()
				continue
			}
			pn := o.Name()
			var uses []*ast.Ident
			for _, st := range x.sel() {
				ast.Inspect(st, func(n ast.Node) bool {
					if id, ok := n.(*ast.Ident); ok && x.info.Uses[id] == o {
						uses = append(uses, id)
					}
					return true
				})
			}
			body.WrapIdents(uses, "(*%s)", pn, "shared "+o.Name())
			exprOf[o] = "(*" + pn + ")"
			shared[o] = true
			params = append(params, pn+" *"+x.typeStr(o.Type()))
			args = append(args, "&"+o.Name())
			beforeCall = append(beforeCall, "var "+o.Name()+" "+x.typeStr(o.Type()))
			x.log = append(x.log, fmt.Sprintf("%-60s", fmt.Sprintf("declare %s at the call site and share it: %s + mutated", o.Name(), why)))
		}
		decl = kept
		for _, o := range decl {
			scope := x.info.Scopes[x.owner]
			if x.owner == ast.Node(x.body) {
				scope = x.info.Scopes[x.ftype] // a function body shares its signature's scope
			}
			if o.Parent() != scope {
				fail("%s is declared inside a nested scope of R but used after it", o.Name())
			}
			rs = append(rs, x.typeStr(o.Type()))
			resultNames = append(resultNames, o.Name())
			resultDecls = append(resultDecls, "var "+o.Name()+" "+x.typeStr(o.Type()))
			lhs = append(lhs, o.Name())
		}
		sig := x.signalPlan(f, body, exprOf, decl)
		if sig != nil {
			rs = append(rs, sig.types...)
			resultDecls = append(resultDecls, sig.decls...)
			lhs = append(lhs, sig.lhs...)
			finalExtra, afterCall = sig.final, sig.after
		}
		if len(rs) == 1 {
			results = rs
		} else if len(rs) > 1 {
			results = []string{"(" + strings.Join(rs, ", ") + ")"}
		}
	}
	x.rewriteDefines(body, exprOf, shared)
	// local types move to package level, in source order
	var moved []*ast.DeclStmt
	seen := map[*ast.DeclStmt]bool{}
	for _, d := range x.hoist {
		if !seen[d] {
			seen[d] = true
			moved = append(moved, d)
		}
	}
	sort.Slice(moved, func(i, j int) bool { return moved[i].Pos() < moved[j].Pos() })
	var typeText strings.Builder
	for _, d := range moved {
		typeText.WriteString("\n\n" + file.Text(file.NodeSpan(d)))
		route(d.Pos()).DeleteStmt(d, "move type to package level")
	}

	bodyText, err := body.Apply()
	if err != nil {
		fail("%v", err)
	}
	tparams, targs := "", ""
	if x.decl.Recv != nil && len(x.decl.Recv.List) > 0 {
		switch x.decl.Recv.List[0].Type.(type) {
		case *ast.IndexExpr, *ast.IndexListExpr:
			fail("the enclosing method has a generic receiver: its type parameters have no declaration to copy")
		}
		if st, ok := x.decl.Recv.List[0].Type.(*ast.StarExpr); ok {
			switch st.X.(type) {
			case *ast.IndexExpr, *ast.IndexListExpr:
				fail("the enclosing method has a generic receiver: its type parameters have no declaration to copy")
			}
		}
	}
	if tp := x.decl.Type.TypeParams; tp != nil && len(tp.List) > 0 {
		tparams = "[" + file.Text(edit.Span{Start: x.off(tp.Opening + 1), End: x.off(tp.Closing)}) + "]"
		var ns []string
		for _, fld := range tp.List {
			for _, n := range fld.Names {
				ns = append(ns, n.Name)
			}
		}
		targs = "[" + strings.Join(ns, ", ") + "]"
		x.log = append(x.log, fmt.Sprintf("%-60s", "copy type parameters "+tparams))
	}
	var fnText strings.Builder
	fmt.Fprintf(&fnText, "\n\nfunc %s%s(%s) %s {\n", name, tparams, strings.Join(params, ", "), strings.Join(results, " "))
	for _, p := range prologue {
		fnText.WriteString(p + "\n")
	}
	fnText.WriteString(bodyText + "\n")
	last := x.sel()[len(x.sel())-1]
	if !tail && len(resultNames)+len(finalExtra) > 0 && !x.terminating(last) {
		fnText.WriteString("return " + strings.Join(append(append([]string{}, resultNames...), finalExtra...), ", ") + "\n")
	}
	fnText.WriteString("}\n")
	call := name + targs + "(" + strings.Join(args, ", ") + ")"
	switch {
	case tail && !x.void():
		call = "return " + call
	case tail && x.terminating(x.sel()[len(x.sel())-1]):
		call += "\nreturn"
	case !tail && len(lhs) > 0:
		call = strings.Join(append(resultDecls, strings.Join(lhs, ", ")+" = "+call), "\n")
	}
	if len(afterCall) > 0 {
		call += "\n" + strings.Join(afterCall, "\n")
	}
	if len(beforeCall) > 0 {
		call = strings.Join(beforeCall, "\n") + "\n" + call
	}
	file.Replace(edit.Span{Start: x.off(a), End: x.off(b)}, call, "call site")
	for path := range x.named {
		if file.EnsureImport(x.file, path) {
			x.log = append(x.log, fmt.Sprintf("%-60s", "import "+path+" (the signature names it)"))
		}
	}
	file.Insert(x.off(x.decl.End()), typeText.String()+fnText.String(), "new function")
	out, err := file.Format()
	if err != nil {
		fail("%v", err)
	}
	return out
}

// Options select growth where an adapter exists, for comparison.
type Options struct {
	Name       string // the new function; default "extracted"
	GrowJumps  bool   // grow R to hold jump targets instead of signalling
	GrowDefers bool   // grow R to the tail instead of registering defers
}

// Result is a done extraction.
type Result struct {
	Source     []byte   // the rewritten file, gofmt'd
	Start, End int      // lines of the final R in the original file
	Tail       bool     // the call is in tail position
	Log        []string // why R grew and which adapters were used
}

var (
	ErrWholeBody     = errors.New("the range is the whole body of the function: nothing to extract")
	ErrNoCode        = errors.New("the range holds only comments or blank lines")
	ErrGrewWholeBody = errors.New("R grew to the whole body: extraction would only wrap it")
)

// Extract moves lines [A, B] of file, in the package in dir, into a new
// function. On a refusal the error says why and Result.Log how R got there.
func Extract(dir, file string, A, B int, opt Options) (res Result, err error) {
	var x *X
	defer func() {
		if r := recover(); r != nil {
			f, ok := r.(failure)
			if !ok {
				panic(r)
			}
			err = errors.New(string(f))
		}
		if x != nil {
			res.Log = x.log
		}
	}()
	if opt.Name == "" {
		opt.Name = "extracted"
	}
	x = load(dir, file)
	x.opt = opt
	// the innermost function (declaration or literal) whose body holds line A
	for _, d := range x.file.Decls {
		decl, ok := d.(*ast.FuncDecl)
		if !ok || decl.Body == nil || !(x.line(decl.Body.Pos()) < A && A <= x.line(decl.Body.End())) {
			continue
		}
		x.index(decl, decl)
		x.pick = func(lo, hi token.Pos) {
			var fn ast.Node = decl
			ast.Inspect(decl.Body, func(n ast.Node) bool {
				if l, ok := n.(*ast.FuncLit); ok && l.Body.Pos() < lo && hi < l.Body.End() {
					fn = l
				}
				return true
			})
			x.index(decl, fn)
		}
		// extent of the whole statements in [A, B], else of those it touches
		lo, hi := token.Pos(1<<62), token.NoPos
		for _, whole := range []bool{true, false} {
			for st := range x.where {
				in := x.line(st.Pos()) >= A && x.line(st.End()) <= B
				touch := x.line(st.Pos()) <= B && x.line(st.End()) >= A
				if whole && in || !whole && touch {
					lo, hi = min(lo, st.Pos()), max(hi, st.End())
				}
			}
			if hi != token.NoPos {
				break
			}
		}
		x.pick(lo, hi)
	}
	if x.fn == nil {
		fail("no function body holds line %d", A)
	}
	// initial R: the whole statements in [A, B]; else the innermost statement
	// holding the range; else the statements the range touches
	inBody := func(st ast.Stmt) bool { return st.Pos() > x.body.Pos() && st.End() < x.body.End() }
	var hit []ast.Node
	for st := range x.where {
		if x.line(st.Pos()) >= A && x.line(st.End()) <= B && inBody(st) {
			hit = append(hit, st)
		}
	}
	if len(hit) == 0 {
		// statements that start or end inside the range: the ones it cuts
		for st := range x.where {
			holds := x.line(st.Pos()) < A && x.line(st.End()) > B
			if x.line(st.Pos()) <= B && x.line(st.End()) >= A && !holds && inBody(st) {
				hit = append(hit, st)
			}
		}
		if len(hit) > 0 {
			x.log = append(x.log, fmt.Sprintf("%-60s", fmt.Sprintf("lines %d-%d cut statements: take the statements they touch", A, B)))
		}
	}
	if len(hit) == 0 {
		// no statement boundary inside the range: the innermost statement holding it
		var best ast.Stmt
		for st := range x.where {
			if x.line(st.Pos()) <= A && x.line(st.End()) >= B && inBody(st) {
				if best == nil || st.End()-st.Pos() < best.End()-best.Pos() {
					best = st
				}
			}
		}
		if best != nil && !isCommentOnly(x, A, B) {
			hit = append(hit, best)
			x.log = append(x.log, fmt.Sprintf("%-60s", fmt.Sprintf("lines %d-%d lie inside one statement: take it (%d-%d)", A, B, x.line(best.Pos()), x.line(best.End()))))
		}
	}
	if len(hit) == 0 {
		return res, fmt.Errorf("lines %d-%d: %w", A, B, ErrNoCode)
	}
	sort.Slice(hit, func(i, j int) bool { return hit[i].Pos() < hit[j].Pos() })
	// the whole body of a declared function: extracting it would only wrap it.
	// (The whole body of a function literal is fine: that lifts the closure.)
	whole := func() bool {
		_, isDecl := x.fn.(*ast.FuncDecl)
		return isDecl && x.owner == ast.Node(x.body) && x.i == 0 && x.j == len(x.body.List)
	}
	try := func(hit []ast.Node) {
		w := x.where[hit[0].(ast.Stmt)]
		x.owner, x.i, x.j = w.owner, w.idx, w.idx+1
		x.cover(fmt.Sprintf("selection %d-%d snapped to statements", A, B), hit...)
		for x.grow() {
		}
	}
	// the range itself holds every statement of a declared function's body
	if _, isDecl := x.fn.(*ast.FuncDecl); isDecl {
		in := map[ast.Node]bool{}
		for _, h := range hit {
			in[h] = true
		}
		all := true
		for _, st := range x.body.List {
			all = all && in[st]
		}
		if all {
			return res, fmt.Errorf("lines %d-%d: %w", A, B, ErrWholeBody)
		}
	}
	try(hit)
	if whole() {
		// the selection crosses block boundaries and covering it all is
		// useless: read it as the statements of the block it starts in
		first := x.where[hit[0].(ast.Stmt)].owner
		var same []ast.Node
		for _, h := range hit {
			if x.where[h.(ast.Stmt)].owner == first {
				same = append(same, h)
			}
		}
		if len(same) < len(hit) {
			x.log = append(x.log, fmt.Sprintf("%-60s", "whole body; the selection crosses blocks: keep the statements of its first block"))
			// those statements may sit in a function literal: choose the function again
			x.pick(same[0].Pos(), same[len(same)-1].End())
			try(same)
		}
	}
	if whole() {
		return res, fmt.Errorf("lines %d-%d: %w", A, B, ErrGrewWholeBody)
	}
	out := x.generate(opt.Name)
	a, b := x.span()
	return Result{Source: out, Start: x.line(a), End: x.line(b), Tail: x.tail()}, nil
}
