package refactor

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"strings"

	"github.com/dhilst/sx/internal/cost"
)

// Inlining a call to a function used once, and then deleting the function,
// changes the tree by
//
//	ΔN = N(R) − N(S) − N(D) + ΔI
//
// S is the syntax the call occupied, R what the inliner puts there, D the
// declaration, and ΔI the imports added and removed. R depends on which of
// gopls's strategies applies (golang.org/x/tools/internal/refactor/inline):
//
//   - a single "return e" in expression position: R is e, with each
//     parameter replaced by its argument;
//   - a call statement: R is the body's statements, preceded by a var
//     declaration binding the parameters that cannot be replaced by their
//     arguments, inside braces only when the names would clash;
//   - anything else is wrapped in a function literal ("literalization"),
//     which keeps the declaration's whole signature and so cannot shrink
//     the tree. The model refuses it.
//
// A parameter is replaced by its argument when that cannot change behaviour
// or style: the callee neither assigns nor takes the address of it, and an
// argument referenced more than once is duplicable (an identifier, an int
// literal, ""...). Each reference then costs N(arg) instead of one node.

// Inlining is the model's account of one inline.
type Inlining struct {
	R, S, D, I int
	Strategy   string
	Bound      []string // parameters kept in a var declaration
}

// Delta is the change in |AST|.
func (m Inlining) Delta() int { return m.R - m.S - m.D + m.I }

func (m Inlining) String() string {
	s := fmt.Sprintf("R=%d S=%d D=%d ΔI=%+d ΔN=%+d (%s", m.R, m.S, m.D, m.I, m.Delta(), m.Strategy)
	if len(m.Bound) > 0 {
		s += "; binds " + strings.Join(m.Bound, ", ")
	}
	return s + ")"
}

// predictInline prices inlining call, found in callerFile, to decl, declared
// in calleeFile.
func predictInline(tp *typedPackage, callerFile *ast.File, call *ast.CallExpr, calleeFile *ast.File, decl *ast.FuncDecl) (Inlining, error) {
	info := tp.info
	m := Inlining{D: cost.Count(decl)}
	if decl.Type.TypeParams != nil {
		return m, fmt.Errorf("generic functions are not modelled")
	}
	if call.Ellipsis.IsValid() {
		return m, fmt.Errorf("spread calls are not modelled")
	}
	path := pathTo(callerFile, call)
	var parent ast.Node
	if len(path) > 1 {
		parent = path[len(path)-2]
	}

	// The parameters, flattened, each with its argument.
	type param struct {
		obj     types.Object
		field   int
		refs    int
		keep    bool
		arg     ast.Expr
		argCost int
	}
	var params []*param
	for fi, field := range decl.Type.Params.List {
		if _, ok := field.Type.(*ast.Ellipsis); ok {
			return m, fmt.Errorf("variadic functions are not modelled")
		}
		for _, name := range field.Names {
			params = append(params, &param{obj: info.Defs[name], field: fi})
		}
	}
	if len(params) != len(call.Args) {
		return m, fmt.Errorf("argument count does not match")
	}
	declared := map[string]bool{} // names the body declares
	ast.Inspect(decl.Body, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			if obj := info.Defs[id]; obj != nil {
				declared[id.Name] = true
			}
		}
		return true
	})
	for i, p := range params {
		p.arg = call.Args[i]
		p.argCost = cost.Count(p.arg)
		assigned, escapes := false, false
		ast.Inspect(decl.Body, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.Ident:
				if info.Uses[n] == p.obj {
					p.refs++
				}
			case *ast.AssignStmt:
				for _, lhs := range n.Lhs {
					if id, ok := lhs.(*ast.Ident); ok && info.Uses[id] == p.obj {
						assigned = true
					}
				}
			case *ast.IncDecStmt:
				if id, ok := n.X.(*ast.Ident); ok && info.Uses[id] == p.obj {
					assigned = true
				}
			case *ast.UnaryExpr:
				if id, ok := n.X.(*ast.Ident); ok && n.Op == token.AND && info.Uses[id] == p.obj {
					escapes = true
				}
			}
			return true
		})
		shadowed := false
		for name := range freeNames(info, p.arg) {
			if declared[name] {
				shadowed = true
			}
		}
		switch {
		case p.obj == nil || p.obj.Name() == "_":
			p.keep = effects(info, p.arg)
		case escapes || assigned || shadowed:
			p.keep = true
		case p.refs > 1 && !duplicable(info, p.arg):
			p.keep = true
		case p.refs == 0 && effects(info, p.arg):
			p.keep = true
		case p.refs == 0 && lastLocalRef(info, path, call, p.arg):
			// Dropping the argument could leave a caller variable declared
			// and not used.
			p.keep = true
		}
		if p.keep {
			m.Bound = append(m.Bound, p.obj.Name())
		}
	}

	// A named result the body refers to is declared too.
	var namedResults []*ast.Field
	if decl.Type.Results != nil {
		for _, f := range decl.Type.Results.List {
			for _, name := range f.Names {
				if referenced(info, decl.Body, info.Defs[name]) {
					namedResults = append(namedResults, f)
					break
				}
			}
		}
	}
	needBinding := len(m.Bound) > 0 || len(namedResults) > 0

	// The binding declaration: var ( p T = arg ... ), one spec per field.
	binding, bindingNames := 0, map[string]bool{}
	if needBinding {
		binding = 2 // DeclStmt, GenDecl
		for fi, field := range decl.Type.Params.List {
			names, values := 0, 0
			var free map[string]bool
			for _, p := range params {
				if p.field == fi && p.keep {
					names++
					values += p.argCost
					for n := range freeNames(info, p.arg) {
						if free == nil {
							free = map[string]bool{}
						}
						free[n] = true
					}
				}
			}
			if names == 0 {
				continue
			}
			for n := range free {
				if bindingNames[n] {
					return m, fmt.Errorf("the binding declaration would shadow %s", n)
				}
			}
			for _, p := range params {
				if p.field == fi && p.keep {
					bindingNames[p.obj.Name()] = true
				}
			}
			binding += 1 + names + cost.Count(field.Type) + values
		}
		for _, f := range namedResults {
			binding += 1 + len(f.Names) + cost.Count(f.Type)
			for _, n := range f.Names {
				bindingNames[n.Name] = true
			}
		}
	}

	// What each substituted parameter costs where the body refers to it:
	// the argument instead of one identifier, parentheses where it binds
	// looser than its context, and an explicit conversion where the
	// parameter's type would otherwise be lost.
	subst := 0
	var converted []ast.Node // types written out by conversions
	for i, p := range params {
		if p.keep || p.obj == nil {
			continue
		}
		subst += p.refs * (p.argCost - 1)
		subst += parensNeeded(info, decl.Body, p.obj, p.arg)
		if n := conversions(tp, call, decl.Body, p.obj, p.arg); n > 0 {
			field := decl.Type.Params.List[params[i].field].Type
			subst += n * (1 + cost.Count(field))
			converted = append(converted, field)
		}
	}

	body := decl.Body.List
	single := len(body) == 1
	var results []ast.Expr
	if single {
		if ret, ok := body[0].(*ast.ReturnStmt); ok && len(ret.Results) > 0 {
			results = ret.Results
		} else {
			single = false
		}
	}
	stmt, _ := parent.(*ast.ExprStmt)
	switch {
	case len(body) == 0 && stmt != nil:
		m.Strategy = "empty body"
		m.S = cost.Count(stmt)
		var kept int
		for _, p := range params {
			if p.keep {
				kept++
				m.R += p.argCost
			}
		}
		if kept > 0 {
			m.R += 1 + kept // _, _ = args
		}
	case single && len(results) == 1 && stmt != nil && !needBinding && validAsStmt(results[0]):
		m.Strategy = "call statement reduced to its returned call"
		m.S = cost.Count(call)
		m.R = cost.Count(results[0]) + subst
	case single && len(results) == 1 && !needBinding && stmt == nil:
		m.Strategy = "expression reduced to the returned expression"
		m.S = cost.Count(call)
		m.R = cost.Count(results[0]) + subst
		if t := decl.Type.Results.List[0].Type; !trivialConversion(info.Types[results[0]].Value, info.TypeOf(results[0]), info.TypeOf(t)) {
			m.R += 1 + cost.Count(t) // T(e): the implicit conversion made explicit
			converted = append(converted, t)
		}
	case stmt != nil && !single && !hasReturn(decl.Body) && !hasDefer(decl.Body) && !hasLabels(decl.Body):
		m.Strategy = "call statement replaced by the body"
		m.S = cost.Count(stmt)
		for _, s := range body {
			m.R += cost.Count(s)
		}
		m.R += subst + binding
		if clash(info, path, stmt, decl, bindingNames) {
			m.R++ // the braces stay
			m.Strategy += " in braces"
		}
	default:
		return m, fmt.Errorf("gopls would wrap the body in a function literal")
	}

	// Imports: the declaration and the call site go, R arrives.
	gone := map[ast.Node]bool{decl: true, m.oldNode(call, stmt): true}
	arrived := append([]ast.Node{decl.Body}, converted...)
	for _, p := range params {
		if p.keep || p.refs > 0 {
			arrived = append(arrived, p.arg)
		}
	}
	for fi, field := range decl.Type.Params.List {
		for _, p := range params {
			if p.field == fi && p.keep {
				arrived = append(arrived, field.Type)
				break
			}
		}
	}
	m.I = importDelta(tp, []*ast.File{callerFile, calleeFile}, gone, map[*ast.File]map[string]bool{callerFile: packagesIn(info, arrived...)})
	return m, nil
}

func (m Inlining) oldNode(call *ast.CallExpr, stmt *ast.ExprStmt) ast.Node {
	if stmt != nil && strings.HasPrefix(m.Strategy, "call statement replaced") || strings.HasPrefix(m.Strategy, "empty") {
		return stmt
	}
	return call
}

// pathTo is the chain of nodes from file down to n.
func pathTo(file *ast.File, n ast.Node) []ast.Node {
	var path, stack []ast.Node
	ast.Inspect(file, func(x ast.Node) bool {
		if path != nil {
			return false
		}
		if x == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, x)
		if x == n {
			path = append([]ast.Node(nil), stack...)
			return false
		}
		return true
	})
	return path
}

// freeNames is the names of the local variables an expression refers to.
func freeNames(info *types.Info, e ast.Expr) map[string]bool {
	out := map[string]bool{}
	ast.Inspect(e, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			ast.Inspect(sel.X, func(m ast.Node) bool {
				if id, ok := m.(*ast.Ident); ok {
					if _, pkg := info.Uses[id].(*types.PkgName); !pkg {
						out[id.Name] = true
					}
				}
				return true
			})
			return false
		}
		if id, ok := n.(*ast.Ident); ok {
			if v, ok := info.Uses[id].(*types.Var); ok && !v.IsField() {
				out[id.Name] = true
			}
		}
		return true
	})
	return out
}

// duplicable follows the inliner: an expression it will write more than once.
func duplicable(info *types.Info, e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.ParenExpr:
		return duplicable(info, e.X)
	case *ast.Ident:
		return true
	case *ast.BasicLit:
		v := info.Types[e].Value
		switch e.Kind {
		case token.INT:
			return true
		case token.STRING:
			return v != nil && constant.StringVal(v) == ""
		case token.FLOAT:
			return v != nil && (constant.Compare(v, token.EQL, constant.MakeFloat64(0)) || constant.Compare(v, token.EQL, constant.MakeFloat64(1)))
		}
	case *ast.UnaryExpr:
		return (e.Op == token.ADD || e.Op == token.SUB) && duplicable(info, e.X)
	case *ast.CompositeLit:
		if len(e.Elts) == 0 {
			switch info.TypeOf(e).Underlying().(type) {
			case *types.Struct, *types.Array:
				return true
			}
		}
	case *ast.CallExpr:
		if tv, ok := info.Types[e.Fun]; ok && tv.IsType() {
			if s, ok := tv.Type.Underlying().(*types.Slice); ok {
				if b, ok := s.Elem().Underlying().(*types.Basic); ok && (b.Kind() == types.Byte || b.Kind() == types.Rune) {
					from, ok := info.TypeOf(e.Args[0]).Underlying().(*types.Basic)
					return !(ok && from.Info()&types.IsString != 0)
				}
			}
			return true
		}
	case *ast.SelectorExpr:
		if sel, ok := info.Selections[e]; ok {
			return !sel.Indirect()
		}
		return true
	}
	return false
}

// effects reports whether evaluating e could change the program's state: a
// call that is not a conversion or a pure builtin, or a channel receive.
func effects(info *types.Info, e ast.Expr) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.UnaryExpr:
			if n.Op == token.ARROW {
				found = true
			}
		case *ast.CallExpr:
			if tv, ok := info.Types[n.Fun]; ok && tv.IsType() {
				break
			}
			if id, ok := ast.Unparen(n.Fun).(*ast.Ident); ok {
				if b, ok := info.Uses[id].(*types.Builtin); ok {
					switch b.Name() {
					case "len", "cap", "complex", "imag", "real", "min", "max", "unsafe.Sizeof":
						return true
					}
				}
			}
			found = true
		}
		return !found
	})
	return found
}

// lastLocalRef reports whether arg holds what may be the last reference to a
// local variable of the calling function.
func lastLocalRef(info *types.Info, path []ast.Node, call *ast.CallExpr, arg ast.Expr) bool {
	var fn ast.Node
	for i := len(path) - 1; i >= 0; i-- {
		if _, ok := path[i].(*ast.FuncDecl); ok {
			fn = path[i]
			break
		}
		if _, ok := path[i].(*ast.FuncLit); ok {
			fn = path[i]
			break
		}
	}
	if fn == nil {
		return false
	}
	found := false
	ast.Inspect(arg, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok || found {
			return !found
		}
		v, ok := info.Uses[id].(*types.Var)
		if !ok || v.IsField() || !(fn.Pos() <= v.Pos() && v.Pos() < fn.End()) {
			return true
		}
		other := false
		ast.Inspect(fn, func(m ast.Node) bool {
			if m == call {
				return false
			}
			if u, ok := m.(*ast.Ident); ok && info.Uses[u] == v {
				other = true
			}
			return !other
		})
		if !other {
			found = true
		}
		return !found
	})
	return found
}

func referenced(info *types.Info, n ast.Node, obj types.Object) bool {
	found := false
	ast.Inspect(n, func(x ast.Node) bool {
		if id, ok := x.(*ast.Ident); ok && obj != nil && info.Uses[id] == obj {
			found = true
		}
		return !found
	})
	return found
}

// parensNeeded counts the parentheses substitution adds: an argument that
// is an operation placed where a tighter-binding operand is expected.
func parensNeeded(info *types.Info, body ast.Node, obj types.Object, arg ast.Expr) int {
	prec := 0
	switch a := arg.(type) {
	case *ast.BinaryExpr:
		prec = a.Op.Precedence()
	case *ast.UnaryExpr, *ast.StarExpr:
		prec = token.UnaryPrec
	default:
		return 0
	}
	n := 0
	var stack []ast.Node
	ast.Inspect(body, func(x ast.Node) bool {
		if x == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		if id, ok := x.(*ast.Ident); ok && info.Uses[id] == obj && len(stack) > 0 {
			switch p := stack[len(stack)-1].(type) {
			case *ast.BinaryExpr:
				if prec < token.UnaryPrec && (p.Op.Precedence() > prec || p.Op.Precedence() == prec && p.Y == id) {
					n++
				}
			case *ast.SelectorExpr, *ast.IndexExpr, *ast.SliceExpr, *ast.TypeAssertExpr:
				n++
			case *ast.CallExpr:
				if p.Fun == id {
					n++
				}
			case *ast.UnaryExpr, *ast.StarExpr:
				if prec < token.UnaryPrec {
					n++
				}
			}
		}
		stack = append(stack, x)
		return true
	})
	return n
}

// trivialConversion follows the inliner: converting a value of type from
// (a constant, when fromValue is set) to type to changes nothing.
func trivialConversion(fromValue constant.Value, from, to types.Type) bool {
	if fromValue != nil {
		var def types.Type
		switch fromValue.Kind() {
		case constant.Bool:
			def = types.Typ[types.Bool]
		case constant.String:
			def = types.Typ[types.String]
		case constant.Int:
			def = types.Typ[types.Int]
		case constant.Float:
			def = types.Typ[types.Float64]
		case constant.Complex:
			def = types.Typ[types.Complex128]
		default:
			return false
		}
		return types.Identical(def, to)
	}
	return from != nil && to != nil && types.Identical(from, to)
}

// conversions counts the references to a substituted parameter at which the
// inliner wraps the argument in a conversion to the parameter's type: where
// the reference is not assigned to something of that type, or is assigned to
// an interface, or feeds type inference, and the argument's own type differs.
func conversions(tp *typedPackage, call *ast.CallExpr, body ast.Node, param types.Object, arg ast.Expr) int {
	info := tp.info
	argType, value := info.TypeOf(arg), info.Types[arg].Value
	if value != nil {
		// The checker gave the constant the parameter's type; the inliner
		// looks at the type it has on its own.
		neutral := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}}
		if err := types.CheckExpr(tp.fset, tp.pkg, call.Pos(), arg, neutral); err == nil {
			argType = neutral.TypeOf(arg)
		}
	}
	if argType == nil || types.Identical(types.Default(argType), param.Type()) {
		return 0
	}
	_, paramIface := param.Type().Underlying().(*types.Interface)
	n := 0
	var stack []ast.Node
	ast.Inspect(body, func(x ast.Node) bool {
		if x == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, x)
		if id, ok := x.(*ast.Ident); ok && info.Uses[id] == param {
			assignable, iface, inference := assignment(info, stack)
			if inference || assignable && iface && !paramIface || !assignable && !trivialConversion(value, argType, param.Type()) {
				n++
			}
		}
		return true
	})
	return n
}

// assignment follows the inliner's analyzeAssignment: whether the expression
// on top of stack is assigned to something, whether that is an interface, and
// whether it feeds type inference.
func assignment(info *types.Info, stack []ast.Node) (assignable, iface, inference bool) {
	expr, _ := stack[len(stack)-1].(ast.Expr)
	var parent ast.Node
	i := len(stack) - 2
	for ; i >= 0; i-- {
		if p, ok := stack[i].(*ast.ParenExpr); ok {
			expr = p
			continue
		}
		parent = stack[i]
		break
	}
	isIface := func(t types.Type) bool { return t == nil || types.IsInterface(t) }
	switch p := parent.(type) {
	case *ast.AssignStmt:
		for j, v := range p.Rhs {
			if v == expr && j < len(p.Lhs) {
				if id, ok := p.Lhs[j].(*ast.Ident); ok && info.Defs[id] != nil {
					return false, false, false
				}
				return true, isIface(info.TypeOf(p.Lhs[j])), false
			}
		}
	case *ast.ValueSpec:
		if p.Type != nil {
			for _, v := range p.Values {
				if v == expr {
					return true, isIface(info.TypeOf(p.Type)), false
				}
			}
		}
	case *ast.IndexExpr:
		if p.Index == expr {
			m, _ := info.TypeOf(p.X).Underlying().(*types.Map)
			return true, m == nil || types.IsInterface(m.Key()), false
		}
	case *ast.SendStmt:
		if p.Value == expr {
			ch, _ := info.TypeOf(p.Chan).Underlying().(*types.Chan)
			return true, ch == nil || types.IsInterface(ch.Elem()), false
		}
	case *ast.CompositeLit:
		for j, v := range p.Elts {
			if v == expr {
				switch u := info.TypeOf(p).Underlying().(type) {
				case interface{ Elem() types.Type }:
					return true, types.IsInterface(u.Elem()), false
				case *types.Struct:
					if j < u.NumFields() {
						return true, types.IsInterface(u.Field(j).Type()), false
					}
				}
				return true, true, false
			}
		}
	case *ast.KeyValueExpr:
		if i > 0 {
			if lit, ok := stack[i-1].(*ast.CompositeLit); ok {
				u := info.TypeOf(lit).Underlying()
				if ptr, ok := u.(*types.Pointer); ok {
					u = ptr.Elem().Underlying()
				}
				if p.Key == expr {
					m, _ := u.(*types.Map)
					return true, m == nil || types.IsInterface(m.Key()), false
				}
				switch u := u.(type) {
				case interface{ Elem() types.Type }:
					return true, types.IsInterface(u.Elem()), false
				case *types.Struct:
					if id, ok := p.Key.(*ast.Ident); ok {
						for f := range u.Fields() {
							if info.Uses[id] == f {
								return true, types.IsInterface(f.Type()), false
							}
						}
					}
				}
				return true, true, false
			}
		}
	case *ast.CallExpr:
		if tv, ok := info.Types[p.Fun]; ok && tv.IsType() {
			return false, false, false
		}
		for j, a := range p.Args {
			if a != expr {
				continue
			}
			sig, _ := info.TypeOf(p.Fun).Underlying().(*types.Signature)
			if sig == nil {
				return true, true, false
			}
			var pt types.Type
			if n := sig.Params().Len(); j < n-1 || j < n && !sig.Variadic() {
				pt = sig.Params().At(j).Type()
			} else if sig.Variadic() && n > 0 {
				if s, ok := sig.Params().At(n - 1).Type().(*types.Slice); ok {
					pt = s.Elem()
				}
			}
			if id, ok := ast.Unparen(p.Fun).(*ast.Ident); ok {
				if b, ok := info.Uses[id].(*types.Builtin); ok {
					switch b.Name() {
					case "new", "complex", "real", "imag", "min", "max":
						inference = true
					}
				}
			}
			return true, isIface(pt), inference
		}
	}
	return false, false, false
}

// validAsStmt reports whether an expression can stand alone as a statement.
func validAsStmt(e ast.Expr) bool {
	switch e := ast.Unparen(e).(type) {
	case *ast.CallExpr:
		return true
	case *ast.UnaryExpr:
		return e.Op == token.ARROW
	}
	return false
}

func hasReturn(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.ReturnStmt:
			found = true
		}
		return !found
	})
	return found
}

func hasDefer(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.DeferStmt:
			found = true
		}
		return !found
	})
	return found
}

func hasLabels(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if _, ok := n.(*ast.LabeledStmt); ok {
			found = true
		}
		return !found
	})
	return found
}

// clash reports whether the inlined statements declare a name the block they
// land in already declares, which is when the inliner keeps the braces.
func clash(info *types.Info, path []ast.Node, stmt *ast.ExprStmt, decl *ast.FuncDecl, binding map[string]bool) bool {
	i := len(path) - 2 // path ends ... parent-of-stmt, stmt, call
	var list []ast.Stmt
	switch p := path[i-1].(type) {
	case *ast.BlockStmt:
		list = p.List
	case *ast.CaseClause:
		list = p.Body
	case *ast.CommClause:
		list = p.Body
	default:
		return true
	}
	caller := topDeclares(list)
	if i-2 >= 0 {
		var ft *ast.FuncType
		var recv *ast.FieldList
		switch f := path[i-2].(type) {
		case *ast.FuncDecl:
			ft, recv = f.Type, f.Recv
		case *ast.FuncLit:
			ft = f.Type
		}
		for _, fl := range []*ast.FieldList{recv, fieldsOf(ft, true), fieldsOf(ft, false)} {
			if fl != nil {
				for _, f := range fl.List {
					for _, n := range f.Names {
						caller[n.Name] = true
					}
				}
			}
		}
	}
	for _, l := range labelsIn(path) {
		_ = l
		return true
	}
	inlined := topDeclares(decl.Body.List)
	for n := range binding {
		inlined[n] = true
	}
	for n := range inlined {
		if caller[n] {
			return true
		}
	}
	return false
}

func fieldsOf(ft *ast.FuncType, params bool) *ast.FieldList {
	if ft == nil {
		return nil
	}
	if params {
		return ft.Params
	}
	return ft.Results
}

// topDeclares is the names a statement list declares at its own level.
func topDeclares(stmts []ast.Stmt) map[string]bool {
	out := map[string]bool{}
	for _, s := range stmts {
		switch s := s.(type) {
		case *ast.AssignStmt:
			if s.Tok == token.DEFINE {
				for _, l := range s.Lhs {
					if id, ok := l.(*ast.Ident); ok {
						out[id.Name] = true
					}
				}
			}
		case *ast.DeclStmt:
			for _, spec := range s.Decl.(*ast.GenDecl).Specs {
				switch spec := spec.(type) {
				case *ast.ValueSpec:
					for _, n := range spec.Names {
						out[n.Name] = true
					}
				case *ast.TypeSpec:
					out[spec.Name.Name] = true
				}
			}
		case *ast.LabeledStmt:
			out[s.Label.Name] = true
		}
	}
	return out
}

// labelsIn is the labels of the function around the path.
func labelsIn(path []ast.Node) []string {
	var fn ast.Node
	for i := len(path) - 1; i >= 0; i-- {
		switch path[i].(type) {
		case *ast.FuncDecl, *ast.FuncLit:
			fn = path[i]
		}
		if fn != nil {
			break
		}
	}
	var out []string
	if fn != nil {
		ast.Inspect(fn, func(n ast.Node) bool {
			if l, ok := n.(*ast.LabeledStmt); ok {
				out = append(out, l.Label.Name)
			}
			return true
		})
	}
	return out
}
