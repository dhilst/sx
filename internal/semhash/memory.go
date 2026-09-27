package semhash

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"sort"
)

// Cells: a local variable lives in memory, rather than in the environment,
// when it can be reached other than by its name: its address is taken
// (explicitly, by a pointer-method call on it, or by slicing an array held
// in it), or a closure captures it and something assigns it after its
// declaration. Its reads and writes are then ordered loads and stores on the
// state, which is what makes aliasing, closures and pointer-passing adapters
// agree with the variable they stand for.

// analyseCells marks the cell variables declared in the function root: those
// whose address is taken (explicitly, by a pointer-method call on them, or by
// slicing an array held in them), and those a closure captures that can be
// assigned after the closure exists - inside the closure, textually after it
// within the variable's scope, or in a loop (inside that scope) holding both
// the assignment and the closure, which runs the assignment again later. A
// captured variable that never changes once captured is held by value.
func (e *evaluator) analyseCells(root ast.Node) {
	assignedAt := map[types.Object][]token.Pos{}
	var lits []*ast.FuncLit
	var loops []ast.Node
	assign := func(x ast.Expr) {
		if o := e.rootVar(x); o != nil {
			assignedAt[o] = append(assignedAt[o], x.Pos())
		}
	}
	address := func(x ast.Expr) {
		if o := e.rootVar(x); o != nil {
			e.cells[o] = true
		}
	}
	ast.Inspect(root, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			for _, l := range n.Lhs {
				if id, ok := l.(*ast.Ident); ok && n.Tok == token.DEFINE && e.s.Info.Defs[id] != nil {
					continue // a declaration, not an assignment
				}
				assign(l)
			}
		case *ast.IncDecStmt:
			assign(n.X)
		case *ast.RangeStmt:
			loops = append(loops, n)
			if n.Tok == token.ASSIGN {
				for _, x := range []ast.Expr{n.Key, n.Value} {
					if x != nil {
						assign(x)
					}
				}
			}
		case *ast.ForStmt:
			loops = append(loops, n)
		case *ast.UnaryExpr:
			if n.Op == token.AND {
				address(n.X)
			}
		case *ast.SelectorExpr:
			if sel := e.s.Info.Selections[n]; sel != nil && sel.Kind() == types.MethodVal {
				recv := sel.Obj().Type().(*types.Signature).Recv()
				if recv != nil && isPointer(recv.Type()) && !isPointer(e.s.Info.TypeOf(n.X)) && !types.IsInterface(e.s.Info.TypeOf(n.X)) {
					address(n.X) // v.M() with M on *T takes &v
				}
			}
		case *ast.SliceExpr:
			if _, ok := e.s.Info.TypeOf(n.X).Underlying().(*types.Array); ok {
				address(n.X)
			}
		case *ast.FuncLit:
			lits = append(lits, n)
		}
		return true
	})
	within := func(p token.Pos, n ast.Node) bool { return p >= n.Pos() && p < n.End() }
	changesAfter := func(v types.Object, lit *ast.FuncLit) bool {
		for _, a := range assignedAt[v] {
			if within(a, lit) || a >= lit.End() {
				return true
			}
			for _, l := range loops {
				if within(lit.Pos(), l) && within(a, l) && l.Pos() > v.Pos() {
					return true
				}
			}
		}
		return false
	}
	for _, lit := range lits {
		ast.Inspect(lit.Body, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				if v, ok := e.s.Info.Uses[id].(*types.Var); ok && e.isLocalVar(v) && !within(v.Pos(), lit) && changesAfter(v, lit) {
					e.cells[v] = true // the closure and the function share it
				}
			}
			return true
		})
	}
}

func (e *evaluator) isLocalVar(v *types.Var) bool {
	return !v.IsField() && !e.isGlobal(v) && v.Pkg() != nil
}

// rootVar is the local variable an addressable expression is part of: x in
// x, x.f (a struct value), x[i] (an array value), (x).
func (e *evaluator) rootVar(x ast.Expr) types.Object {
	for {
		switch t := x.(type) {
		case *ast.ParenExpr:
			x = t.X
		case *ast.Ident:
			o := e.s.Info.Uses[t]
			if o == nil {
				o = e.s.Info.Defs[t]
			}
			if v, ok := o.(*types.Var); ok && e.isLocalVar(v) {
				return v
			}
			return nil
		case *ast.SelectorExpr:
			sel := e.s.Info.Selections[t]
			if sel == nil || sel.Kind() != types.FieldVal || sel.Indirect() {
				return nil
			}
			x = t.X
		case *ast.IndexExpr:
			if _, ok := e.s.Info.TypeOf(t.X).Underlying().(*types.Array); !ok {
				return nil
			}
			x = t.X
		default:
			return nil
		}
	}
}

// A cell's address is its identity, allocated where it is declared. Until the
// address escapes - &v, a pointer-method call, a closure capturing it, or a
// loop (conservatively) - nothing but the function can read or write it, so
// its value is kept in the environment (under a shadow key) like any local.
// When the address escapes, the value is written to memory and from then on
// every read and write is an ordered load or store.

type cellKeys struct{ shadow, escaped, placeholder *types.Var }

func (e *evaluator) keysOf(v types.Object) cellKeys {
	k, ok := e.cellKey[v]
	if !ok {
		k = cellKeys{types.NewVar(token.NoPos, nil, v.Name()+"#value", v.Type()), types.NewVar(token.NoPos, nil, v.Name()+"#escaped", types.Typ[types.Bool]),
			types.NewVar(token.NoPos, nil, v.Name()+"#placeholder", types.NewPointer(v.Type()))}
		e.cellKey[v] = k
		e.cellOf[k.escaped] = v
		e.cellOf[k.placeholder] = v
	}
	return k
}

// declare binds a variable to its first value: a cell is allocated (its
// value kept locally until its address escapes), anything else goes straight
// into the environment.
func (e *evaluator) declare(v types.Object, val Node) {
	if e.cells[v] {
		k := e.keysOf(v)
		p := e.unallocated(v)
		e.cellAt[p] = v
		e.env[v], e.env[k.placeholder] = p, p
		e.env[k.shadow] = e.norm(val, v.Type())
		e.env[k.escaped] = e.in.F
		return
	}
	e.env[v] = e.norm(val, v.Type())
}

// A cell is allocated when its address first leaves the function's hands -
// reaches an effect, a closure or the result - not where it is declared:
// allocating is an effect on the state, and allocation is unobservable
// until then, so a declaration or an &v that moves (into or out of an
// extracted function) must not move it. Until then its address is a
// placeholder, unique to this declaration being evaluated; resolve replaces
// placeholders of allocated cells by their addresses in what reaches an
// effect. A placeholder left in a term is unique to its side and never
// equal to anything on the other: it can only make a proof fail.
func (e *evaluator) unallocated(v types.Object) Node {
	e.placeholders++
	return e.in.mk(opConst, "unallocated", fmt.Sprintf("%p.%d", e, e.placeholders))
}

func (e *evaluator) isUnallocated(n Node) bool { return e.in.t(n).typ == "unallocated" }

// allocate gives cell v its address, if it has none yet.
func (e *evaluator) allocate(v types.Object) Node {
	if e.isUnallocated(e.env[v]) {
		addr := e.effect("newcell:"+e.key(v.Type()), []types.Type{types.NewPointer(v.Type())})[0]
		e.cellAt[addr] = v
		e.env[v] = addr
	}
	return e.env[v]
}

// resolve replaces the placeholders of cells allocated since by their
// addresses.
func (e *evaluator) resolve(ns []Node) []Node {
	memo := map[Node]Node{}
	var walk func(n Node) Node
	walk = func(n Node) Node {
		if r, ok := memo[n]; ok {
			return r
		}
		r := n
		t := e.in.t(n)
		switch t.op {
		case opConst:
			if t.typ == "unallocated" {
				if v, ok := e.cellAt[n]; ok && e.env[e.keysOf(v).placeholder] == n && !e.isUnallocated(e.env[v]) {
					r = e.env[v]
				}
			}
		case opMux:
			a, hi, lo := walk(t.kids[0]), walk(t.kids[1]), walk(t.kids[2])
			if a != t.kids[0] || hi != t.kids[1] || lo != t.kids[2] {
				r = e.in.ite(e.in.atom(a), hi, lo)
			}
		case opPure, opTuple, opOpaque:
			kids := make([]Node, len(t.kids))
			changed := false
			for i, k := range t.kids {
				kids[i] = walk(k)
				changed = changed || kids[i] != k
			}
			if changed {
				r = e.in.mk(t.op, t.typ, t.aux, kids...)
			}
		}
		memo[n] = r
		return r
	}
	out := make([]Node, len(ns))
	for i, n := range ns {
		out[i] = walk(n)
	}
	return out
}

func (e *evaluator) readVar(v types.Object) Node {
	if e.cells[v] {
		k := e.keysOf(v)
		if e.env[k.escaped] == e.in.F {
			return e.env[k.shadow]
		}
		return e.effect("load", []types.Type{v.Type()}, e.env[v])[0]
	}
	return e.env[v]
}

func (e *evaluator) writeVar(v types.Object, val Node) {
	if e.cells[v] {
		k := e.keysOf(v)
		if e.env[k.escaped] == e.in.F {
			e.env[k.shadow] = e.norm(val, v.Type())
			return
		}
		e.effect("store", nil, e.env[v], val)
		return
	}
	e.env[v] = e.norm(val, v.Type())
}

// escape writes a cell's local value to memory: from here on its address is
// out of the function's hands.
func (e *evaluator) escape(v types.Object) {
	k := e.keysOf(v)
	if e.env[k.escaped] != e.in.F {
		return
	}
	addr := e.allocate(v)
	e.env[k.escaped] = e.in.T // first, so the store below goes to memory
	e.effect("store", nil, addr, e.env[k.shadow])
}

// localCell: n is the address of a cell whose address has not escaped.
func (e *evaluator) localCell(n Node) (types.Object, bool) {
	v, ok := e.cellAt[n]
	if !ok {
		return nil, false
	}
	if e.env[v] != n || e.env[e.keysOf(v).escaped] != e.in.F {
		return nil, false
	}
	return v, true
}

// escapeReachable: any local cell whose address a value carries (inside
// conversions, field addresses, struct values, method values, branch
// values) leaves the function's hands when that value goes somewhere the
// evaluator does not follow. Values produced by effects were already
// checked when they were made.
func (e *evaluator) escapeReachable(ns ...Node) {
	seen := map[Node]bool{}
	var walk func(n Node)
	walk = func(n Node) {
		if n < 0 || seen[n] {
			return
		}
		seen[n] = true
		if v, ok := e.localCell(n); ok {
			e.escape(v)
			return
		}
		switch t := e.in.t(n); t.op {
		case opPure, opMux, opTuple, opOpaque:
			for _, k := range t.kids {
				walk(k)
			}
		}
	}
	for _, n := range ns {
		walk(n)
	}
}

// escapeAll: before a loop, every cell the loop can reach goes to memory
// (the body may take addresses in an order the evaluator does not track). A
// loop reaches a cell by naming it, or through its address held in some
// other local value; a cell it cannot reach keeps its local value, which the
// loop cannot change.
func (e *evaluator) escapeAll(loop ast.Node) {
	named := map[types.Object]bool{}
	ast.Inspect(loop, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			if o := e.s.Info.Uses[id]; o != nil {
				named[o] = true
			}
		}
		return true
	})
	addrs := map[Node]bool{}
	for _, v := range sortedObjs(e.env) {
		if e.cells[v] {
			if _, ok := e.localCell(e.env[v]); ok {
				addrs[e.env[v]] = true
			}
		}
	}
	held := map[Node]bool{} // addresses some other value carries
	seen := map[Node]bool{}
	var walk func(Node)
	walk = func(x Node) {
		if x < 0 || seen[x] {
			return
		}
		seen[x] = true
		if addrs[x] {
			held[x] = true
		}
		for _, k := range e.in.t(x).kids {
			walk(k)
		}
	}
	for k, val := range e.env { // (only marks: the order does not matter)
		if v, ok := e.cellOf[k.(*types.Var)]; ok && e.keysOf(v).placeholder == k {
			continue // the cell's own bookkeeping, not a copy of its address
		}
		if !e.cells[k] { // a cell's own entry is its address, not a copy
			walk(val)
		}
	}
	for _, v := range sortedObjs(e.env) {
		if e.cells[v] && (named[v] || held[e.env[v]]) {
			e.escape(v)
		}
	}
}

// flushDisagreeing: where paths meet, a cell escaped on one path and not on
// another is written to memory on the paths where it had not escaped, so
// that all agree it lives in memory.
func (e *evaluator) flushDisagreeing(parts []part) {
	// every cell any part knows, in a fixed order: the stores this adds are
	// effects, and effects must not depend on map iteration
	seenCell := map[types.Object]bool{}
	var cellsInParts []types.Object
	for _, p := range parts {
		for key := range p.env {
			if v, ok := e.cellOf[key.(*types.Var)]; ok && !seenCell[v] {
				seenCell[v] = true
				cellsInParts = append(cellsInParts, v)
			}
		}
	}
	sort.Slice(cellsInParts, func(i, j int) bool {
		a, b := cellsInParts[i], cellsInParts[j]
		return a.Pos() < b.Pos() || a.Pos() == b.Pos() && a.Name() < b.Name()
	})
	for _, v := range cellsInParts {
		key := types.Object(e.keysOf(v).escaped)
		{
			esc := false
			for _, q := range parts {
				if q.env[key] == e.in.T {
					esc = true
				}
			}
			if !esc {
				continue
			}
			for i := range parts {
				if parts[i].env[key] == e.in.F {
					k := e.keysOf(v)
					if e.isUnallocated(parts[i].env[v]) {
						pt := types.NewPointer(v.Type())
						n := e.in.mk(opEffect, "effect", "newcell:"+e.key(v.Type()), parts[i].st)
						addr := e.norm(e.in.mk(opProj, e.key(pt), "0", n), pt)
						parts[i].st = e.in.mk(opProj, "state", "1", n)
						e.cellAt[addr] = v
						parts[i].env[v] = addr
					}
					n := e.in.mk(opEffect, "effect", "store", parts[i].st, parts[i].env[v], parts[i].env[k.shadow])
					parts[i].st = e.in.mk(opProj, "state", "0", n)
					parts[i].env[key] = e.in.T
				}
			}
		}
	}
}

// addressOf evaluates &x.
func (e *evaluator) addressOf(x ast.Expr) Node {
	switch t := ast.Unparen(x).(type) {
	case *ast.Ident:
		o := e.s.Info.Uses[t]
		if o == nil {
			o = e.s.Info.Defs[t]
		}
		v, ok := o.(*types.Var)
		if !ok {
			refuse("the address of %s at %s", t.Name, e.pos(t))
		}
		if e.isGlobal(v) {
			return e.in.mk(opPure, e.key(types.NewPointer(v.Type())), "globaladdr:"+objKey(v))
		}
		if !e.cells[v] {
			refuse("the address of %s, which is not a cell, at %s", v.Name(), e.pos(t))
		}
		return e.env[v] // escapes (and is allocated) when it reaches something the evaluator does not follow
	case *ast.SelectorExpr:
		sel := e.s.Info.Selections[t]
		if sel == nil {
			return e.addressOf(t.Sel)
		}
		if sel.Indirect() {
			return e.fieldAddr(e.expr(t.X), e.typeOf(t.X), sel.Index())
		}
		return e.fieldAddr(e.addressOf(t.X), types.NewPointer(e.typeOf(t.X)), sel.Index())
	case *ast.IndexExpr:
		et := types.NewPointer(e.typeOf(t))
		switch e.typeOf(t.X).Underlying().(type) {
		case *types.Array:
			return e.effect("indexaddr", []types.Type{et}, e.addressOf(t.X), e.expr(t.Index))[0]
		}
		return e.effect("indexaddr", []types.Type{et}, e.expr(t.X), e.expr(t.Index))[0]
	case *ast.StarExpr:
		return e.expr(t.X)
	case *ast.CompositeLit:
		pt := types.NewPointer(e.typeOf(t))
		return e.effect("new:"+e.key(pt), []types.Type{pt}, e.composite(t))[0]
	}
	refuse("the address of %T at %s", x, e.pos(x))
	return -1
}

// funcLit: a closure is a lambda term - its body evaluated with its
// parameters and the state it will run in as bound variables, reading what it
// captures from where it is created: the values of variables nothing assigns
// again, the cells of variables it shares. Two closures with the same term
// compute the same thing, whatever their code looks like.
func (e *evaluator) funcLit(x *ast.FuncLit) Node {
	sig := e.typeOf(x).(*types.Signature)
	// cells the closure captures leave the function's hands now
	ast.Inspect(x.Body, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			if v, ok := e.s.Info.Uses[id].(*types.Var); ok && e.isLocalVar(v) && !(v.Pos() >= x.Pos() && v.Pos() < x.End()) {
				if _, in := e.env[v]; !in {
					refuse("a closure captures %s, which is not in scope here (%s)", v.Name(), e.pos(x))
				}
				if e.cells[v] {
					e.escape(v)
				}
				e.escapeReachable(e.env[v]) // a captured pointer to a local cell
			}
		}
		return true
	})
	env, exits, fsig, named, outer, g, st := e.env, e.exits, e.sig, e.named, e.outer, e.g, e.st
	defers, fdepth, reach := e.defers, e.frameDepth, e.reach
	e.lambda++
	level := e.lambda
	defer e.pushFrame(frameOwn)()
	defer func() {
		e.env, e.exits, e.sig, e.named, e.outer, e.g, e.st = env, exits, fsig, named, outer, g, st
		e.defers, e.frameDepth, e.reach = defers, fdepth, reach
		e.lambda--
	}()
	e.env = e.cloneEnv()
	e.g, e.reach = e.in.T, e.in.T
	e.st = e.in.mk(opBound, "state", fmt.Sprintf("λ%d.st", level))
	params := sig.Params()
	for i := 0; i < params.Len(); i++ {
		p := params.At(i)
		e.declare(p, e.in.mk(opBound, e.key(p.Type()), fmt.Sprintf("λ%d.%d", level, i)))
	}
	body := e.body(sig, x.Body)
	return e.in.mk(opLambda, e.key(sig), fmt.Sprintf("λ%d", level), body)
}

// callLiteral evaluates func(...){...}(args) in place: the literal's body
// runs as a frame that sees the variables in scope.
func (e *evaluator) callLiteral(lit *ast.FuncLit, x *ast.CallExpr) []Node {
	sig := e.typeOf(lit).(*types.Signature)
	args := e.args(x, sig)
	env, exits, fsig, named, outer, g := e.env, e.exits, e.sig, e.named, e.outer, e.g
	defers, fdepth := e.defers, e.frameDepth
	e.literal++
	defer func() { e.defers, e.frameDepth = defers, fdepth; e.literal-- }()
	defer e.pushFrame(frameInlined)()
	frameCells := e.frameCells
	e.env = e.cloneEnv()
	e.frameCells = e.callerCells(env, map[types.Object]Node{})
	params := sig.Params()
	for i := 0; i < params.Len(); i++ {
		e.declare(params.At(i), args[i])
	}
	r := e.body(sig, lit.Body)
	cellOut := e.cellsOut
	e.env, e.exits, e.sig, e.named, e.outer, e.g = env, exits, fsig, named, outer, g
	e.frameCells, e.cellsOut = frameCells, nil
	e.returnedCells(cellOut, g)
	r = e.in.under(r, g)
	n := sig.Results().Len()
	out := make([]Node, n)
	for i := 0; i < n; i++ {
		t := sig.Results().At(i).Type()
		out[i] = e.norm(e.proj(r, i, e.key(t)), t)
	}
	e.st = e.proj(r, n, "state")
	return out
}

// Cells across inlined frames. A helper evaluated in place, or a literal
// called in place, can reach the caller's cells (through an address passed
// to it, or by capture). It starts with the caller's cell bookkeeping and,
// when it returns, hands back the cells' state merged over its exits (see
// cellsAcross), so the caller continues exactly where the callee left them.

// callerCells copies the bookkeeping of the caller's cells into env and
// lists the cells that are still local.
func (e *evaluator) callerCells(from, into map[types.Object]Node) []types.Object {
	var local []types.Object
	for _, v := range sortedObjs(from) {
		if !e.cells[v] {
			continue
		}
		k := e.keysOf(v)
		into[v] = from[v]
		into[k.shadow] = from[k.shadow]
		into[k.escaped] = from[k.escaped]
		into[k.placeholder] = from[k.placeholder]
		if from[k.escaped] == e.in.F {
			local = append(local, v)
		}
	}
	return local
}

// returnedCells: after an inlined frame, the caller continues with the
// frame's merged cell state.
func (e *evaluator) returnedCells(out map[types.Object]Node, g Node) {
	for _, key := range sortedObjs(out) {
		e.env[key] = e.in.under(out[key], g)
	}
}
