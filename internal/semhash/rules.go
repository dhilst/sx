package semhash

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
)

// Rules are the equalities the normaliser knows beyond the language: facts
// about the standard library, each justified below. They rewrite the forms
// sx's eg templates replace into the forms the templates produce, on both
// sides alike. A template is never an axiom by itself: only the identities
// written here are used, so adding a template adds nothing to what can be
// proved until its rule is written and justified.
//
//   strings.Index(s, t) >= 0      strings.Contains(s, t)    Contains is implemented as Index(s, t) >= 0
//   strings.Index(s, t) == -1     !strings.Contains(s, t)   Index returns -1 or a position
//   len(s) == 0 (s a string)      s == ""                   a string is empty iff its length is 0
//   len(s) != 0, len(s) > 0       s != ""
//   s[:len(s)]                    s                         the same string or slice header, and cannot panic
//   bytes.Compare(a, b) == 0      bytes.Equal(a, b)         Equal is documented as Compare(a, b) == 0
//   strconv.FormatInt(int64(i),10) strconv.Itoa(i)           Itoa is implemented as FormatInt(int64(i), 10)
//   fmt.Sprintf("%d", i) (int i)  strconv.Itoa(i)           %d of a plain int is its decimal form
//   fmt.Sprintf("%s", s) (string) s                         %s of a plain string is the string
//   fmt.Errorf("%s", s) (string)  errors.New(s)             Errorf without %w is errors.New(Sprintf(...))
//   time.Now().Sub(t)             time.Since(t)             an axiom: Since reads the clock as Now does

// pureFuncs are standard functions that neither read nor write memory the
// program can see, cannot panic and return no fresh reference (slice, map,
// pointer): a call to one is a pure operation on its arguments.
var pureFuncTerms = map[string]bool{
	"strings.Index": true, "strings.IndexByte": true, "strings.IndexRune": true, "strings.LastIndex": true,
	"strings.Contains": true, "strings.ContainsRune": true, "strings.ContainsAny": true,
	"strings.HasPrefix": true, "strings.HasSuffix": true, "strings.EqualFold": true, "strings.Compare": true,
	"strings.Count": true, "strings.TrimSpace": true, "strings.TrimPrefix": true, "strings.TrimSuffix": true,
	"strings.Trim": true, "strings.TrimLeft": true, "strings.TrimRight": true,
	"strings.ToUpper": true, "strings.ToLower": true,
	"strconv.Itoa": true, "strconv.FormatInt": true, "strconv.FormatBool": true, "strconv.Quote": true,
}

func funcObj(s *Snapshot, x ast.Expr) *types.Func {
	if id := identOf(ast.Unparen(x)); id != nil {
		f, _ := s.Info.Uses[id].(*types.Func)
		return f
	}
	return nil
}

func funcName(f *types.Func) string {
	if f == nil || f.Pkg() == nil {
		return ""
	}
	if sig, ok := f.Type().(*types.Signature); ok && sig.Recv() != nil {
		return ""
	}
	return f.Pkg().Path() + "." + f.Name()
}

// callTo: x is a call of the named package function.
func (e *evaluator) callTo(x ast.Expr, name string) (*ast.CallExpr, bool) {
	c, ok := ast.Unparen(x).(*ast.CallExpr)
	if !ok || funcName(funcObj(e.s, c.Fun)) != name {
		return nil, false
	}
	return c, true
}

func (e *evaluator) constInt(x ast.Expr) (int64, bool) {
	tv := e.s.Info.Types[x]
	if tv.Value == nil || tv.Value.Kind() != constant.Int {
		return 0, false
	}
	return constant.Int64Val(tv.Value)
}

func (e *evaluator) constString(x ast.Expr) (string, bool) {
	tv := e.s.Info.Types[x]
	if tv.Value == nil || tv.Value.Kind() != constant.String {
		return "", false
	}
	return constant.StringVal(tv.Value), true
}

// exactly: x's type is the predeclared basic type k itself (not a named type,
// which could carry String or Format methods).
func (e *evaluator) exactly(x ast.Expr, k types.BasicKind) bool {
	b, ok := e.typeOf(x).(*types.Basic)
	return ok && b.Kind() == k
}

// ruleCompare rewrites comparisons the rules know. ok is false if none applies.
func (e *evaluator) ruleCompare(x *ast.BinaryExpr) (Node, bool) {
	k, isK := e.constInt(x.Y)
	// strings.Index(s, t) against -1 or 0
	if c, ok := e.callTo(x.X, "strings.Index"); ok && isK {
		contains := func() Node { return e.pureCall("strings.Contains", c.Args, types.Typ[types.Bool]) }
		switch {
		case x.Op == token.GEQ && k == 0, x.Op == token.NEQ && k == -1, x.Op == token.GTR && k == -1:
			return contains(), true
		case x.Op == token.EQL && k == -1, x.Op == token.LSS && k == 0:
			return e.in.not(contains()), true
		}
	}
	// len(s) against 0, for a string s
	if c, ok := ast.Unparen(x.X).(*ast.CallExpr); ok && isK && k == 0 && len(c.Args) == 1 {
		if b, ok := e.s.Info.Uses[identOf(c.Fun)].(*types.Builtin); ok && b.Name() == "len" {
			if st, ok := e.typeOf(c.Args[0]).Underlying().(*types.Basic); ok && st.Info()&types.IsString != 0 {
				t := e.typeOf(c.Args[0])
				s := e.expr(c.Args[0])
				empty := e.in.mk(opConst, e.key(t), `""`)
				switch x.Op {
				case token.EQL:
					return e.compare(token.EQL, s, empty, t, t, x), true
				case token.NEQ, token.GTR:
					return e.compare(token.NEQ, s, empty, t, t, x), true
				}
			}
		}
	}
	// bytes.Compare(a, b) against 0
	if c, ok := e.callTo(x.X, "bytes.Compare"); ok && isK && k == 0 && (x.Op == token.EQL || x.Op == token.NEQ) {
		a, b := e.expr(c.Args[0]), e.expr(c.Args[1])
		eq := e.effect("call:bytes.Equal", []types.Type{types.Typ[types.Bool]}, a, b)[0]
		if x.Op == token.NEQ {
			return e.in.not(eq), true
		}
		return eq, true
	}
	return -1, false
}

// ruleCall rewrites calls the rules know.
func (e *evaluator) ruleCall(x *ast.CallExpr) ([]Node, bool) {
	name := funcName(funcObj(e.s, x.Fun))
	switch name {
	case "strconv.FormatInt":
		if len(x.Args) == 2 {
			if base, ok := e.constInt(x.Args[1]); ok && base == 10 {
				if conv, ok := ast.Unparen(x.Args[0]).(*ast.CallExpr); ok && len(conv.Args) == 1 && e.s.Info.Types[conv.Fun].IsType() &&
					e.exactly(conv, types.Int64) && e.exactly(conv.Args[0], types.Int) {
					return []Node{e.pureCall("strconv.Itoa", conv.Args, types.Typ[types.String])}, true
				}
			}
		}
	case "fmt.Sprintf", "fmt.Errorf":
		if len(x.Args) != 2 || x.Ellipsis.IsValid() {
			break
		}
		format, ok := e.constString(x.Args[0])
		if !ok {
			break
		}
		switch {
		case name == "fmt.Sprintf" && format == "%d" && e.exactly(x.Args[1], types.Int):
			return []Node{e.pureCall("strconv.Itoa", x.Args[1:], types.Typ[types.String])}, true
		case name == "fmt.Sprintf" && format == "%s" && e.exactly(x.Args[1], types.String):
			return []Node{e.expr(x.Args[1])}, true
		case name == "fmt.Errorf" && format == "%s" && e.exactly(x.Args[1], types.String):
			return e.effect("call:errors.New", []types.Type{e.typeOf(x)}, e.expr(x.Args[1])), true
		}
	}
	// time.Now().Sub(t)
	if sel, ok := ast.Unparen(x.Fun).(*ast.SelectorExpr); ok && len(x.Args) == 1 {
		if s := e.s.Info.Selections[sel]; s != nil && objKey(s.Obj()) == "time.Time.Sub" {
			if _, ok := e.callTo(sel.X, "time.Now"); ok {
				e.axioms["time.Since(t) behaves as time.Now().Sub(t)"] = true
				return e.effect("call:time.Since", []types.Type{e.typeOf(x)}, e.expr(x.Args[0])), true
			}
		}
	}
	if name == "time.Since" {
		e.axioms["time.Since(t) behaves as time.Now().Sub(t)"] = true
	}
	return nil, false
}

// pureCall builds a call of a pure standard function on evaluated arguments.
func (e *evaluator) pureCall(name string, args []ast.Expr, result types.Type) Node {
	vals := make([]Node, len(args))
	for i, a := range args {
		vals[i] = e.expr(a)
	}
	return e.pure("call:"+name, result, vals...)
}

// readOnly: effects that read memory but never write it. A load of an
// address already loaded, with only such effects in between, cannot panic
// where the first did not and reads the same value.
func readOnlyEffect(name string) bool {
	switch name {
	case "load", "index", "mapget", "len", "cap":
		return true
	}
	return false
}

// reuseLoad looks back along the state for the same read of the same
// operands, through reads only, and returns that read's effect node.
func (e *evaluator) reuseLoad(name string, operands []Node) (Node, bool) {
	st := e.st
	for steps := 0; steps < 64; steps++ {
		t := e.in.t(st)
		if t.op != opProj || len(t.kids) != 1 {
			return -1, false
		}
		n := e.in.t(t.kids[0])
		if n.op != opEffect || !readOnlyEffect(n.aux) {
			return -1, false
		}
		if n.aux == name && len(n.kids) == len(operands)+1 {
			same := true
			for i, o := range operands {
				if n.kids[i+1] != o {
					same = false
				}
			}
			if same {
				return t.kids[0], true
			}
		}
		st = n.kids[0]
	}
	return -1, false
}
