package refactor

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"reflect"
	"strconv"

	"github.com/dhilst/sx/internal/cost"
)

// An eg template rewrites every match of before(w...) into after(w...). A
// match binds each wildcard w to an expression m_w, so with b_w and a_w the
// times w appears in before and in after,
//
//	ΔN = Σ_matches [ N(after) − N(before) + Σ_w (a_w − b_w)·(N(m_w) − 1) ] + ΔI
//
// A wildcard that appears as often on both sides costs nothing whatever it
// binds; one that after drops (s[:len(s)] → s) saves its binding. eg adds the
// imports after needs and the repair step removes the ones before no longer
// uses, which is ΔI. Only matches in files the measure counts are counted:
// eg rewrites tests too, but the measure does not see them.

// Rewrite is the model's account of one template applied to the tree.
type Rewrite struct {
	Matches int
	P       int // N(after) − N(before)
	W       int // the wildcard terms, summed over the matches
	I       int
}

// Delta is the change in |AST|.
func (m Rewrite) Delta() int { return m.Matches*m.P + m.W + m.I }

func (m Rewrite) String() string {
	return fmt.Sprintf("matches=%d P=%+d W=%+d ΔI=%+d ΔN=%+d", m.Matches, m.P, m.W, m.I, m.Delta())
}

// template is an eg template read for the model.
type template struct {
	before, after ast.Expr
	wild          map[string]ast.Expr // wildcard name -> its declared type
	imports       map[string]string   // local name -> path
}

func readTemplate(path string) (*template, error) {
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return nil, err
	}
	t := &template{wild: map[string]ast.Expr{}, imports: map[string]string{}}
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		name := filepath.Base(p)
		if imp.Name != nil {
			name = imp.Name.Name
		}
		t.imports[name] = p
	}
	var beforeParams, afterParams []*ast.Ident
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		var names []*ast.Ident
		for _, field := range fn.Type.Params.List {
			for _, n := range field.Names {
				names = append(names, n)
				if fn.Name.Name == "before" {
					t.wild[n.Name] = field.Type
				}
			}
		}
		switch fn.Name.Name {
		case "before":
			beforeParams = names
		case "after":
			afterParams = names
		}
	}
	b, err := templateExpr(f, "before")
	if err != nil {
		return nil, err
	}
	a, err := templateExpr(f, "after")
	if err != nil {
		return nil, err
	}
	t.before, t.after = b.(ast.Expr), a.(ast.Expr)
	// after's parameters are before's, by position.
	if len(afterParams) == len(beforeParams) {
		rename := map[string]string{}
		for i, n := range afterParams {
			rename[n.Name] = beforeParams[i].Name
		}
		ast.Inspect(t.after, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				if to, ok := rename[id.Name]; ok {
					id.Name = to
				}
			}
			return true
		})
	}
	return t, nil
}

// occurrences counts each wildcard in e.
func (t *template) occurrences(e ast.Expr) map[string]int {
	out := map[string]int{}
	ast.Inspect(e, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			if _, ok := t.wild[id.Name]; ok {
				out[id.Name]++
			}
		}
		return true
	})
	return out
}

// predictEg prices applying one template to every file under dir that the
// measure counts.
func predictEg(ps packages, dir, templatePath string) (Rewrite, error) {
	t, err := readTemplate(templatePath)
	if err != nil {
		return Rewrite{}, err
	}
	m := Rewrite{P: cost.Count(t.after) - cost.Count(t.before)}
	b, a := t.occurrences(t.before), t.occurrences(t.after)
	files, err := goFilesIn(dir)
	if err != nil {
		return m, err
	}
	for _, path := range files {
		if !inCurrentBuild(path) {
			continue
		}
		tp, err := ps.load(filepath.Dir(path))
		if err != nil {
			continue
		}
		f := tp.files[path]
		gone := map[ast.Node]bool{}
		arrived := map[string]bool{}
		ast.Inspect(t.after, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				if id, ok := sel.X.(*ast.Ident); ok {
					if p, ok := t.imports[id.Name]; ok {
						arrived[p] = true
					}
				}
			}
			return true
		})
		ast.Inspect(f, func(n ast.Node) bool {
			e, ok := n.(ast.Expr)
			if !ok {
				return true
			}
			bind := map[string]ast.Expr{}
			if !t.match(tp, t.before, e, bind) {
				return true
			}
			if generated(path) {
				err = fmt.Errorf("eg would rewrite generated file %s", filepath.Base(path))
			}
			m.Matches++
			gone[e] = true
			for w, x := range bind {
				m.W += (a[w] - b[w]) * (cost.Count(x) - 1)
				if a[w] > 0 {
					for p := range packagesIn(tp.info, x) {
						arrived[p] = true
					}
				}
			}
			return false
		})
		if err != nil {
			return m, err
		}
		if len(gone) > 0 {
			m.I += importDelta(tp, []*ast.File{f}, gone, map[*ast.File]map[string]bool{f: arrived})
		}
	}
	return m, nil
}

// match reports whether the code expression x matches the pattern p,
// binding wildcards as it goes.
func (t *template) match(tp *typedPackage, p ast.Node, x ast.Node, bind map[string]ast.Expr) bool {
	if id, ok := p.(*ast.Ident); ok {
		if typ, ok := t.wild[id.Name]; ok {
			xe, ok := x.(ast.Expr)
			if !ok {
				return false
			}
			if prev, ok := bind[id.Name]; ok {
				return equalNodes(prev, xe)
			}
			if want := t.resolve(tp, typ); want != nil {
				got := tp.info.TypeOf(xe)
				if got == nil || !types.AssignableTo(got, want) {
					return false
				}
			}
			bind[id.Name] = xe
			return true
		}
	}
	// pkg.Name in the template matches the same object however the file
	// imports its package.
	if sel, ok := p.(*ast.SelectorExpr); ok {
		if id, ok := sel.X.(*ast.Ident); ok {
			if path, ok := t.imports[id.Name]; ok {
				xs, ok := x.(*ast.SelectorExpr)
				if !ok || xs.Sel.Name != sel.Sel.Name {
					return false
				}
				xid, ok := xs.X.(*ast.Ident)
				if !ok {
					return false
				}
				pn, ok := tp.info.Uses[xid].(*types.PkgName)
				return ok && pn.Imported().Path() == path
			}
		}
	}
	if x, ok := x.(*ast.ParenExpr); ok {
		if _, ok := p.(*ast.ParenExpr); !ok {
			return t.match(tp, p, x.X, bind)
		}
	}
	pv, xv := reflect.ValueOf(p), reflect.ValueOf(x)
	if pv.Type() != xv.Type() {
		return false
	}
	return t.matchValue(tp, pv.Elem(), xv.Elem(), bind)
}

func (t *template) matchValue(tp *typedPackage, p, x reflect.Value, bind map[string]ast.Expr) bool {
	switch p.Kind() {
	case reflect.Struct:
		for i := 0; i < p.NumField(); i++ {
			if !skipField(p.Type().Field(i)) && !t.matchValue(tp, p.Field(i), x.Field(i), bind) {
				return false
			}
		}
		return true
	case reflect.Slice:
		if p.Len() != x.Len() {
			return false
		}
		for i := 0; i < p.Len(); i++ {
			if !t.matchValue(tp, p.Index(i), x.Index(i), bind) {
				return false
			}
		}
		return true
	case reflect.Interface, reflect.Pointer:
		if p.IsNil() || x.IsNil() {
			return p.IsNil() == x.IsNil()
		}
		if n, ok := p.Interface().(ast.Node); ok {
			xn, ok := x.Interface().(ast.Node)
			return ok && t.match(tp, n, xn, bind)
		}
		return t.matchValue(tp, p.Elem(), x.Elem(), bind)
	}
	return p.Interface() == x.Interface()
}

func skipField(f reflect.StructField) bool {
	switch f.Type {
	case reflect.TypeOf(token.NoPos), reflect.TypeOf((*ast.Object)(nil)), reflect.TypeOf((*ast.Scope)(nil)), reflect.TypeOf((*ast.CommentGroup)(nil)):
		return true
	}
	return false
}

// equalNodes is structural equality, positions aside.
func equalNodes(a, b ast.Node) bool {
	t := &template{wild: map[string]ast.Expr{}}
	return t.match(nil, a, b, nil)
}

// resolve is the type a wildcard declares, looked up from the target
// package: its own imports, or the universe.
func (t *template) resolve(tp *typedPackage, e ast.Expr) types.Type {
	if tp == nil {
		return nil
	}
	switch e := e.(type) {
	case *ast.Ident:
		if obj, ok := types.Universe.Lookup(e.Name).(*types.TypeName); ok {
			return obj.Type()
		}
	case *ast.SelectorExpr:
		id, ok := e.X.(*ast.Ident)
		if !ok {
			return nil
		}
		path := t.imports[id.Name]
		for _, imp := range tp.pkg.Imports() {
			if imp.Path() == path {
				if obj, ok := imp.Scope().Lookup(e.Sel.Name).(*types.TypeName); ok {
					return obj.Type()
				}
			}
		}
	case *ast.ArrayType:
		if elem := t.resolve(tp, e.Elt); elem != nil && e.Len == nil {
			return types.NewSlice(elem)
		}
	case *ast.StarExpr:
		if elem := t.resolve(tp, e.X); elem != nil {
			return types.NewPointer(elem)
		}
	case *ast.MapType:
		k, v := t.resolve(tp, e.Key), t.resolve(tp, e.Value)
		if k != nil && v != nil {
			return types.NewMap(k, v)
		}
	case *ast.InterfaceType:
		if len(e.Methods.List) == 0 {
			return types.NewInterfaceType(nil, nil)
		}
	}
	return nil
}
