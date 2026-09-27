package semhash

import (
	"fmt"
	"go/ast"
	"go/types"
	"sort"
	"strings"
)

// Keys name objects and types the same way in two type-checks of two
// versions of a package, which types.Identical cannot (it compares pointers).

// funcKey: pkg.F, pkg.T.M, pkg.(*T).M.
func (s *Snapshot) funcKey(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return s.ImportPath + "." + fd.Name.Name
	}
	return objKey(s.Info.Defs[fd.Name])
}

// objKey names a package-level object, a method, a field (by owner and index
// path is not needed here: fields are keyed by name within their struct type
// key) or a builtin. Local objects never get a key: they are bound in the
// evaluator's environment.
func objKey(o types.Object) string {
	if o == nil {
		return "<nil>"
	}
	if o.Pkg() == nil {
		return "builtin." + o.Name()
	}
	if f, ok := o.(*types.Func); ok {
		if sig, ok := f.Type().(*types.Signature); ok && sig.Recv() != nil {
			return f.Pkg().Path() + "." + recvName(sig.Recv().Type()) + "." + f.Name()
		}
		return f.Pkg().Path() + "." + f.Name()
	}
	return o.Pkg().Path() + "." + o.Name()
}

func recvName(t types.Type) string {
	ptr := false
	if p, ok := t.(*types.Pointer); ok {
		t, ptr = p.Elem(), true
	}
	name := ""
	switch n := types.Unalias(t).(type) {
	case *types.Named:
		name = n.Origin().Obj().Name()
	case *types.Interface:
		name = "interface"
	default:
		name = t.String()
	}
	if ptr {
		return "(*" + name + ")"
	}
	return name
}

// typeKey is a canonical, version-independent spelling of a type. Local named
// types are qualified by the function declaring them.
func (s *Snapshot) typeKey(t types.Type) string {
	var b strings.Builder
	s.writeType(&b, t, map[types.Type]bool{})
	return b.String()
}

func (s *Snapshot) writeType(b *strings.Builder, t types.Type, seen map[types.Type]bool) {
	switch t := t.(type) {
	case nil:
		b.WriteString("<nil>")
	case *types.Alias:
		s.writeType(b, types.Unalias(t), seen)
	case *types.Basic:
		b.WriteString(t.Name())
	case *types.Named:
		o := t.Origin().Obj()
		switch {
		case o.Pkg() == nil:
			b.WriteString(o.Name()) // error, comparable
		case o.Parent() == o.Pkg().Scope():
			b.WriteString(o.Pkg().Path() + "." + o.Name())
		default:
			b.WriteString("local:" + s.enclosing(o.Parent()) + "." + o.Name())
		}
		if args := t.TypeArgs(); args != nil && args.Len() > 0 {
			b.WriteString("[")
			for i := 0; i < args.Len(); i++ {
				if i > 0 {
					b.WriteString(",")
				}
				s.writeType(b, args.At(i), seen)
			}
			b.WriteString("]")
		}
	case *types.TypeParam:
		fmt.Fprintf(b, "typeparam:%s#%d", t.Obj().Name(), t.Index())
	case *types.Pointer:
		b.WriteString("*")
		s.writeType(b, t.Elem(), seen)
	case *types.Slice:
		b.WriteString("[]")
		s.writeType(b, t.Elem(), seen)
	case *types.Array:
		fmt.Fprintf(b, "[%d]", t.Len())
		s.writeType(b, t.Elem(), seen)
	case *types.Map:
		b.WriteString("map[")
		s.writeType(b, t.Key(), seen)
		b.WriteString("]")
		s.writeType(b, t.Elem(), seen)
	case *types.Chan:
		fmt.Fprintf(b, "chan%d ", t.Dir())
		s.writeType(b, t.Elem(), seen)
	case *types.Signature:
		b.WriteString("func(")
		s.writeTuple(b, t.Params(), t.Variadic(), seen)
		b.WriteString(")(")
		s.writeTuple(b, t.Results(), false, seen)
		b.WriteString(")")
	case *types.Tuple:
		b.WriteString("(")
		s.writeTuple(b, t, false, seen)
		b.WriteString(")")
	case *types.Struct:
		b.WriteString("struct{")
		for i := 0; i < t.NumFields(); i++ {
			f := t.Field(i)
			if f.Embedded() {
				b.WriteString("embed ")
			}
			if !f.Exported() && f.Pkg() != nil {
				b.WriteString(f.Pkg().Path() + ".")
			}
			b.WriteString(f.Name() + " ")
			s.writeType(b, f.Type(), seen)
			if tag := t.Tag(i); tag != "" {
				fmt.Fprintf(b, " %q", tag)
			}
			b.WriteString(";")
		}
		b.WriteString("}")
	case *types.Interface:
		b.WriteString("interface{")
		var ms []string
		for i := 0; i < t.NumMethods(); i++ {
			m := t.Method(i)
			var mb strings.Builder
			if !m.Exported() && m.Pkg() != nil {
				mb.WriteString(m.Pkg().Path() + ".")
			}
			mb.WriteString(m.Name())
			s.writeType(&mb, m.Type(), seen)
			ms = append(ms, mb.String())
		}
		sort.Strings(ms)
		b.WriteString(strings.Join(ms, ";"))
		for i := 0; i < t.NumEmbeddeds(); i++ {
			if _, isNamed := t.EmbeddedType(i).(*types.Named); isNamed {
				continue // its methods are listed above
			}
			b.WriteString(";embed ")
			s.writeType(b, t.EmbeddedType(i), seen)
		}
		b.WriteString("}")
	case *types.Union:
		b.WriteString("union(")
		for i := 0; i < t.Len(); i++ {
			if t.Term(i).Tilde() {
				b.WriteString("~")
			}
			s.writeType(b, t.Term(i).Type(), seen)
			b.WriteString("|")
		}
		b.WriteString(")")
	default:
		fmt.Fprintf(b, "?%T", t)
	}
}

func (s *Snapshot) writeTuple(b *strings.Builder, t *types.Tuple, variadic bool, seen map[types.Type]bool) {
	for i := 0; i < t.Len(); i++ {
		if i > 0 {
			b.WriteString(",")
		}
		if variadic && i == t.Len()-1 {
			b.WriteString("...")
		}
		s.writeType(b, t.At(i).Type(), seen)
	}
}

// enclosing names the function whose scope (or a scope inside it) holds sc.
func (s *Snapshot) enclosing(sc *types.Scope) string {
	for ; sc != nil; sc = sc.Parent() {
		if k, ok := s.funcScope[sc]; ok {
			return k
		}
	}
	return "?"
}
