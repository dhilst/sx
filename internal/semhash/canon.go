package semhash

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"reflect"
	"strings"
)

// canon spells a syntax tree with everything that does not change its meaning
// taken out: positions, comments, parentheses around a single operand, the
// spelling of constants, and names. Identifiers become what they resolve to:
// package-level objects by key, locals by the order in which the tree defines
// them, labels likewise. Two trees with equal canon differ only in those.
type canonizer struct {
	s      *Snapshot
	b      strings.Builder
	locals map[types.Object]int
	labels map[string]int
}

func (s *Snapshot) canon(n ast.Node) string {
	c := &canonizer{s: s, locals: map[types.Object]int{}, labels: map[string]int{}}
	c.node(reflect.ValueOf(n))
	return c.b.String()
}

var (
	posType     = reflect.TypeOf(token.NoPos)
	commentType = reflect.TypeOf((*ast.CommentGroup)(nil))
	objType     = reflect.TypeOf((*ast.Object)(nil))
	scopeType   = reflect.TypeOf((*ast.Scope)(nil))
)

func (c *canonizer) node(v reflect.Value) {
	if !v.IsValid() {
		return
	}
	switch v.Kind() {
	case reflect.Interface, reflect.Pointer:
		if v.IsNil() {
			c.b.WriteString("nil")
			return
		}
		if v.Type() == commentType || v.Type() == objType || v.Type() == scopeType {
			return
		}
		if n, ok := v.Interface().(ast.Node); ok && c.special(n) {
			return
		}
		c.node(v.Elem())
	case reflect.Struct:
		t := v.Type()
		c.b.WriteString(t.Name() + "{")
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.Type == posType || f.Type == commentType || f.Type == objType || f.Type == scopeType {
				continue
			}
			c.b.WriteString(f.Name + ":")
			c.node(v.Field(i))
			c.b.WriteString(";")
		}
		c.b.WriteString("}")
	case reflect.Slice:
		c.b.WriteString("[")
		for i := 0; i < v.Len(); i++ {
			if i > 0 {
				c.b.WriteString(",")
			}
			c.node(v.Index(i))
		}
		c.b.WriteString("]")
	case reflect.String:
		fmt.Fprintf(&c.b, "%q", v.String())
	default:
		fmt.Fprintf(&c.b, "%v", v.Interface())
	}
}

// special handles the nodes whose spelling is not their meaning.
func (c *canonizer) special(n ast.Node) bool {
	switch n := n.(type) {
	case *ast.ParenExpr:
		c.node(reflect.ValueOf(n.X))
		return true
	case *ast.BasicLit:
		c.constant(n)
		return true
	case *ast.Ident:
		c.ident(n)
		return true
	case *ast.SelectorExpr:
		if tv, ok := c.s.Info.Types[n]; ok && tv.Value != nil {
			c.constant(n)
			return true
		}
		if sel := c.s.Info.Selections[n]; sel != nil {
			fmt.Fprintf(&c.b, "Sel{%d;%v;", sel.Kind(), sel.Index())
			c.node(reflect.ValueOf(n.X))
			fmt.Fprintf(&c.b, ";%s;%s}", c.s.typeKey(sel.Recv()), sel.Obj().Name())
			return true
		}
		c.ident(n.Sel) // pkg.Name: the object key already says which package
		return true
	case *ast.LabeledStmt:
		fmt.Fprintf(&c.b, "Labeled{%s;", c.label(n.Label.Name))
		c.node(reflect.ValueOf(n.Stmt))
		c.b.WriteString("}")
		return true
	case *ast.BranchStmt:
		fmt.Fprintf(&c.b, "Branch{%s;", n.Tok)
		if n.Label != nil {
			c.b.WriteString(c.label(n.Label.Name))
		}
		c.b.WriteString("}")
		return true
	case *ast.ImportSpec:
		fmt.Fprintf(&c.b, "Import{%s}", n.Path.Value)
		return true
	}
	if e, ok := n.(ast.Expr); ok {
		if tv, ok := c.s.Info.Types[e]; ok && tv.Value != nil {
			c.constant(e)
			return true
		}
	}
	return false
}

func (c *canonizer) constant(e ast.Expr) {
	tv := c.s.Info.Types[e]
	if tv.Value == nil {
		if bl, ok := e.(*ast.BasicLit); ok { // a struct tag or an import path
			fmt.Fprintf(&c.b, "Lit{%q}", bl.Value)
		}
		return
	}
	fmt.Fprintf(&c.b, "Const{%s;%s}", c.s.typeKey(tv.Type), tv.Value.ExactString())
}

func (c *canonizer) label(name string) string {
	if _, ok := c.labels[name]; !ok {
		c.labels[name] = len(c.labels)
	}
	return fmt.Sprintf("label#%d", c.labels[name])
}

func (c *canonizer) ident(id *ast.Ident) {
	if id.Name == "_" {
		c.b.WriteString("_")
		return
	}
	o := c.s.Info.Defs[id]
	if o == nil {
		o = c.s.Info.Uses[id]
	}
	if o == nil {
		o = c.s.Info.Implicits[id]
	}
	switch o := o.(type) {
	case nil:
		fmt.Fprintf(&c.b, "Unresolved{%s}", id.Name) // a package clause, a method name in an interface
	case *types.PkgName:
		fmt.Fprintf(&c.b, "Pkg{%s}", o.Imported().Path())
	case *types.Var:
		if o.IsField() {
			fmt.Fprintf(&c.b, "Field{%s}", o.Name())
			return
		}
		if c.isLocal(o) {
			c.local(o)
			return
		}
		fmt.Fprintf(&c.b, "Obj{%s}", objKey(o))
	case *types.Const, *types.TypeName, *types.Label:
		if c.isLocal(o) {
			c.local(o)
			return
		}
		fmt.Fprintf(&c.b, "Obj{%s}", objKey(o))
	default:
		fmt.Fprintf(&c.b, "Obj{%s}", objKey(o))
	}
}

func (c *canonizer) isLocal(o types.Object) bool {
	return o.Pkg() != nil && o.Parent() != nil && o.Parent() != o.Pkg().Scope() && o.Parent() != types.Universe
}

func (c *canonizer) local(o types.Object) {
	if _, ok := c.locals[o]; !ok {
		c.locals[o] = len(c.locals)
	}
	fmt.Fprintf(&c.b, "Local{%d}", c.locals[o])
}
