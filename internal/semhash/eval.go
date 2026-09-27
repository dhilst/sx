package semhash

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
)

// The evaluator turns a function body into one term: the function's results
// and final state, as a multi-terminal BDD over the conditions that decide
// which path ran.
//
//   - Local variables live in an environment keyed by their types.Object, so
//     names, copies and parameter passing leave no trace in the term.
//   - Everything with an effect, or that may panic, consumes the current state
//     and yields a new one, so the order of effects is part of the term. Only
//     operations that can neither observe nor change memory nor panic are pure.
//     A panic is a mode of the state: effects after it are skipped by the
//     semantics, so values computed after a panic are junk on both sides alike.
//   - Control flow is guarded: a statement runs under the path guard; a
//     return, break or continue leaves an exit carrying its guard; branches
//     merge by ite on their guards.
//   - A call to a function the change added or removed is evaluated in place
//     (inlined symbolically); any other call stays a named effect. See
//     Prove for why that is sound.

// exit is control leaving a statement list other than by falling through.
type exit struct {
	kind   token.Token // RETURN, BREAK, CONTINUE
	target ast.Stmt    // the switch or loop a break/continue leaves
	guard  Node
	env    map[types.Object]Node
	st     Node
	vals   []Node // results of a return
	tag    string // inside a loop: "next" or "done"
	label  string // a forward goto's label
}

type evaluator struct {
	in     *interner
	s      *Snapshot
	inline map[string]*ast.FuncDecl // functions this side has and the other does not
	stack  []string                 // inlined calls in progress

	env   map[types.Object]Node
	st    Node // the current state (an MTBDD over the path guards)
	g     Node // the path guard: when control is here
	exits []exit
	sig   *types.Signature
	named []*types.Var          // the current function's named results
	outer []ast.Stmt            // enclosing breakable statements, innermost last
	label map[ast.Stmt]ast.Stmt // labelled statement -> the statement it labels

	root         ast.Node              // the function being evaluated (for liveness)
	cells        map[types.Object]bool // variables that live in memory
	depth        int                   // loop nesting
	forceCarried []types.Object        // set when re-evaluating a pruned loop
	cellKey      map[types.Object]cellKeys
	cellOf       map[*types.Var]types.Object // escaped-flag key -> its cell
	defers       []deferRec                  // the current frame's registered defers
	frameDepth   int                         // loop depth where the current frame starts
	literal      int                         // > 0 inside an inlined function literal
	axioms       map[string]bool             // library facts a proof used
	lambda       int                         // closure nesting, for lambda binders
	frames       []frameKind                 // how each enclosing frame runs, innermost last
	cellAt       map[Node]types.Object       // a cell's address -> the cell
	frameCells   []types.Object              // cells an inlined frame received locally
	cellsOut     map[types.Object]Node       // their state when the frame returns
	gotoLoops    map[string]ast.Stmt         // labels a backward goto jumps to -> their synthetic loop
	placeholders int                         // cell address placeholders made (see unallocated)
	reach        Node                        // the path that entered the enclosing loops (their guards are relative to it)
}

// frameKind: a function body evaluated as its own frame (the function being
// proved, a closure's body) or inlined into another frame (a helper the
// change added or removed, a function literal called in place).
type frameKind int

const (
	frameOwn frameKind = iota
	frameInlined
)

func (e *evaluator) pushFrame(k frameKind) func() {
	e.frames = append(e.frames, k)
	return func() { e.frames = e.frames[:len(e.frames)-1] }
}

func newEvaluator(in *interner, s *Snapshot, inline map[string]*ast.FuncDecl) *evaluator {
	return &evaluator{in: in, s: s, inline: inline, label: map[ast.Stmt]ast.Stmt{}, cells: map[types.Object]bool{},
		cellKey: map[types.Object]cellKeys{}, cellOf: map[*types.Var]types.Object{}, axioms: map[string]bool{},
		cellAt: map[Node]types.Object{}, gotoLoops: map[string]ast.Stmt{}, reach: in.T}
}

func (e *evaluator) key(t types.Type) string { return e.s.typeKey(t) }
func (e *evaluator) typeOf(x ast.Expr) types.Type {
	t := e.s.Info.TypeOf(x)
	if t == nil {
		refuse("no type for an expression at %s", e.pos(x))
	}
	return t
}
func (e *evaluator) pos(n ast.Node) string {
	p := e.s.Fset.Position(n.Pos())
	return fmt.Sprintf("%s:%d", filepath.Base(p.Filename), p.Line)
}

// ---------------------------------------------------------------------------
// functions

// function evaluates fd from its parameters to its result term.
func (e *evaluator) function(fd *ast.FuncDecl) Node {
	sig := e.s.Info.Defs[fd.Name].Type().(*types.Signature)
	e.env = map[types.Object]Node{}
	e.root = fd
	e.analyseCells(fd)
	e.st = e.in.mk(opState0, "state", "")
	e.g = e.in.T
	if fd.Recv != nil && sig.Recv() != nil {
		if obj := e.recvObj(fd); obj != nil {
			e.declare(obj, e.in.mk(opParam, e.key(obj.Type()), "-1"))
		}
	}
	for i := 0; i < sig.Params().Len(); i++ {
		p := sig.Params().At(i)
		e.declare(p, e.in.mk(opParam, e.key(p.Type()), strconv.Itoa(i)))
	}
	return e.body(sig, fd.Body)
}

func (e *evaluator) recvObj(fd *ast.FuncDecl) types.Object {
	for _, n := range fd.Recv.List[0].Names {
		return e.s.Info.Defs[n]
	}
	return nil
}

// body runs a function body in the current environment (parameters bound)
// and folds its exits into one term: an MTBDD of (results..., state) tuples.
func (e *evaluator) body(sig *types.Signature, body *ast.BlockStmt) Node {
	e.sig = sig
	e.named = nil
	for i := 0; i < sig.Results().Len(); i++ {
		r := sig.Results().At(i)
		if r.Name() != "" {
			e.named = append(e.named, r)
			e.declare(r, e.zero(r.Type()))
		}
	}
	e.exits = nil
	e.outer = nil
	e.defers = nil
	e.frameDepth = e.depth
	e.block(body.List)
	if e.g != e.in.F {
		e.runDefers()
		// Falling off the end: a function without results, or one whose last
		// statement panics (the state is then panicking and the results junk).
		var vals []Node
		for i := 0; i < sig.Results().Len(); i++ {
			vals = append(vals, e.zero(sig.Results().At(i).Type()))
		}
		e.exits = append(e.exits, exit{kind: token.RETURN, guard: e.g, st: e.st, vals: vals, env: e.cloneEnv()})
	}
	for _, x := range e.exits {
		if x.kind == token.GOTO {
			refuse("a goto to %s this evaluator cannot follow", x.label)
		}
	}
	if e.frameCells != nil {
		e.cellsAcross()
	}
	return e.fold(e.exits)
}

// cellsAcross: the state of the cells an inlined frame received, merged over
// the frame's exits, for the caller to continue with. Where exits disagree
// on whether a cell escaped, the cell is written to memory on the exits that
// held it locally (their states change before they are folded).
func (e *evaluator) cellsAcross() {
	parts := make([]part, len(e.exits))
	for i, x := range e.exits {
		parts[i] = part{x.guard, x.st, x.env}
	}
	e.flushDisagreeing(parts)
	for i := range e.exits {
		e.exits[i].st, e.exits[i].env = parts[i].st, parts[i].env
	}
	e.cellsOut = map[types.Object]Node{}
	if len(parts) == 0 {
		return
	}
	for _, v := range e.frameCells {
		k := e.keysOf(v)
		for _, key := range []types.Object{k.escaped, k.shadow, v} { // v: its address, if the frame allocated it
			get := func(p part) Node {
				v, ok := p.env[key]
				if !ok {
					refuse("internal: an exit lost the state of a cell (%s)", key.Name())
				}
				return v
			}
			acc := get(parts[len(parts)-1])
			for i := len(parts) - 2; i >= 0; i-- {
				acc = e.in.ite(parts[i].g, get(parts[i]), acc)
			}
			e.cellsOut[key] = acc
		}
	}
}

// fold combines exits, whose guards are disjoint, into one MTBDD of tuples.
func (e *evaluator) fold(exits []exit) Node {
	if len(exits) == 0 {
		return e.in.mk(opTuple, "tuple", "unreachable")
	}
	var acc Node = -1
	for i := len(exits) - 1; i >= 0; i-- {
		x := exits[i]
		t := e.tuple(append(append([]Node(nil), x.vals...), x.st))
		if acc < 0 {
			acc = t
		} else {
			acc = e.in.ite(x.guard, t, acc)
		}
	}
	return acc
}

func (e *evaluator) tuple(elems []Node) Node {
	return e.in.lift(elems, func(l []Node) Node { return e.in.mk(opTuple, "tuple", "", l...) })
}

func (e *evaluator) proj(t Node, i int, typ string) Node {
	return e.in.lift([]Node{t}, func(l []Node) Node {
		if tt := e.in.t(l[0]); tt.op == opTuple && tt.aux == "" {
			return tt.kids[i]
		}
		return e.in.mk(opProj, typ, strconv.Itoa(i), l[0])
	})
}

// ---------------------------------------------------------------------------
// values

func (e *evaluator) zero(t types.Type) Node {
	if b, ok := t.Underlying().(*types.Basic); ok && b.Info()&types.IsBoolean != 0 {
		return e.in.F
	}
	return e.in.mk(opZero, e.key(t), "")
}

func isBool(t types.Type) bool {
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Info()&types.IsBoolean != 0
}

// norm puts a value in canonical form: booleans are BDDs.
func (e *evaluator) norm(v Node, t types.Type) Node {
	if isBool(t) {
		return e.in.atom(v)
	}
	return v
}

func (e *evaluator) constant(tv types.TypeAndValue) Node {
	if isBool(tv.Type) {
		if constant.BoolVal(tv.Value) {
			return e.in.T
		}
		return e.in.F
	}
	return e.in.mk(opConst, e.key(tv.Type), tv.Value.ExactString())
}

// conv converts v from one type to another the way assignment does.
func (e *evaluator) conv(v Node, from, to types.Type) Node {
	if from == nil || to == nil {
		return v
	}
	if b, ok := from.(*types.Basic); ok && b.Kind() == types.UntypedNil {
		return e.zero(to)
	}
	fk, tk := e.key(from), e.key(to)
	if fk == tk || isBool(from) && isBool(to) && !types.IsInterface(to) {
		return v
	}
	if b, ok := from.(*types.Basic); ok && b.Info()&types.IsUntyped != 0 {
		return v // an untyped constant already carries its final type
	}
	return e.in.lift([]Node{v}, func(l []Node) Node { return e.in.mk(opPure, tk, "conv:"+fk, l[0]) })
}

// effect consumes the state and yields results and a new state.
func (e *evaluator) effect(name string, results []types.Type, operands ...Node) []Node {
	// a load or store through the address of a cell that has not escaped is
	// the cell's own value
	if (name == "load" || name == "store") && len(operands) > 0 {
		if v, ok := e.localCell(operands[0]); ok {
			k := e.keysOf(v)
			if name == "load" {
				return []Node{e.norm(e.env[k.shadow], v.Type())}
			}
			e.escapeReachable(operands[1]) // an address stored into a cell is followed no further
			e.env[k.shadow] = e.norm(operands[1], v.Type())
			return nil
		}
	}
	e.escapeReachable(operands...)
	operands = e.resolve(operands)
	if readOnlyEffect(name) && len(results) == 1 {
		if n, ok := e.reuseLoad(name, operands); ok { // the same read again: its value, no new state
			return []Node{e.norm(e.in.mk(opProj, e.key(results[0]), "0", n), results[0])}
		}
	}
	n := e.in.mk(opEffect, "effect", name, append([]Node{e.st}, operands...)...)
	vals := make([]Node, len(results))
	for i, t := range results {
		vals[i] = e.norm(e.in.mk(opProj, e.key(t), strconv.Itoa(i), n), t)
	}
	e.st = e.in.mk(opProj, "state", strconv.Itoa(len(results)), n)
	return vals
}

func (e *evaluator) pure(name string, t types.Type, operands ...Node) Node {
	k := e.key(t)
	return e.norm(e.in.lift(operands, func(l []Node) Node { return e.in.mk(opPure, k, name, l...) }), t)
}

// ---------------------------------------------------------------------------
// statements

func (e *evaluator) block(list []ast.Stmt) {
	for i, s := range list {
		if l, ok := s.(*ast.LabeledStmt); ok {
			e.resumeAt(l.Label.Name) // forward gotos arrive here
		}
		if e.g == e.in.F {
			continue // unreachable, unless a later label is a goto's target
		}
		if l, back := e.backwardLabel(list, i); back && e.gotoLoops[l.Label.Name] == nil {
			e.gotoLoop(l, list[i:])
			return
		}
		e.stmt(s)
	}
}

func (e *evaluator) stmt(s ast.Stmt) {
	switch s := s.(type) {
	case *ast.EmptyStmt:
	case *ast.ExprStmt:
		if e.hazard(s.X) {
			e.opaqueStmt(s)
			return
		}
		e.exprs(s.X)
	case *ast.AssignStmt:
		var ops []ast.Expr
		for _, l := range s.Lhs {
			ops = append(ops, lhsOperands(l)...)
		}
		if e.hazard(append(ops, s.Rhs...)...) {
			e.opaqueStmt(s)
			return
		}
		e.assign(s)
	case *ast.IncDecStmt:
		if e.hazard(lhsOperands(s.X)...) {
			e.opaqueStmt(s)
			return
		}
		op := token.ADD
		if s.Tok == token.DEC {
			op = token.SUB
		}
		get, set := e.lvalue(s.X)
		t := e.typeOf(s.X)
		one := e.in.mk(opConst, e.key(t), "1")
		set(e.binary(op, get(), one, t, t, t, s))
	case *ast.DeclStmt:
		if g, ok := s.Decl.(*ast.GenDecl); ok && g.Tok == token.VAR {
			for _, sp := range g.Specs {
				if e.hazard(sp.(*ast.ValueSpec).Values...) {
					e.opaqueStmt(s)
					return
				}
			}
		}
		e.decl(s)
	case *ast.ReturnStmt:
		e.ret(s)
	case *ast.BlockStmt:
		e.block(s.List)
	case *ast.IfStmt:
		if s.Init != nil {
			e.stmt(s.Init)
		}
		c := e.condOf(s.Cond)
		e.branch(c, func() { e.block(s.Body.List) }, func() {
			if s.Else != nil {
				e.stmt(s.Else)
			}
		})
	case *ast.SwitchStmt:
		e.switchStmt(s)
	case *ast.TypeSwitchStmt:
		e.typeSwitch(s)
	case *ast.LabeledStmt:
		e.label[s] = s.Stmt
		e.stmt(s.Stmt)
	case *ast.BranchStmt:
		e.branchStmt(s)
	case *ast.SendStmt:
		if e.hazard(s.Chan, s.Value) {
			e.opaqueStmt(s)
			return
		}
		ch := e.expr(s.Chan)
		elem := e.typeOf(s.Chan).Underlying().(*types.Chan).Elem()
		e.effect("send", nil, ch, e.conv(e.expr(s.Value), e.typeOf(s.Value), elem))
	case *ast.ForStmt:
		e.forStmt(s)
	case *ast.RangeStmt:
		e.rangeStmt(s)
	case *ast.DeferStmt:
		e.deferStmt(s)
	case *ast.GoStmt:
		e.goStmt(s)
	case *ast.SelectStmt:
		e.selectStmt(s)
	default:
		refuse("%T is not modelled (%s)", s, e.pos(s))
	}
}

func (e *evaluator) decl(s *ast.DeclStmt) {
	g := s.Decl.(*ast.GenDecl)
	if g.Tok != token.VAR {
		return // constants are folded where they are used; local types need nothing
	}
	for _, sp := range g.Specs {
		vs := sp.(*ast.ValueSpec)
		var vals []Node
		var types_ []types.Type
		switch {
		case len(vs.Values) == 0:
		case len(vs.Values) == 1 && len(vs.Names) > 1:
			vals, types_ = e.exprsTyped(vs.Values[0])
		default:
			for _, v := range vs.Values {
				vals = append(vals, e.expr(v))
				types_ = append(types_, e.typeOf(v))
			}
		}
		for i, n := range vs.Names {
			obj := e.s.Info.Defs[n]
			if obj == nil {
				continue // _
			}
			if vals == nil {
				e.declare(obj, e.zero(obj.Type()))
			} else {
				e.declare(obj, e.conv(vals[i], types_[i], obj.Type()))
			}
		}
	}
}

func (e *evaluator) ret(s *ast.ReturnStmt) {
	res := e.sig.Results()
	var vals []Node
	switch {
	case len(s.Results) > 0 && e.hazard(s.Results...):
		vs := e.opaqueExprs(s.Results)
		for i := range vs {
			vals = append(vals, e.conv(vs[i], resultType(s.Results, i, e), res.At(i).Type()))
		}
	case len(s.Results) == 0:
		for _, r := range e.named {
			vals = append(vals, e.readVar(r))
		}
	case len(s.Results) == 1 && res.Len() > 1:
		vs, ts := e.exprsTyped(s.Results[0])
		for i := range vs {
			vals = append(vals, e.conv(vs[i], ts[i], res.At(i).Type()))
		}
	default:
		for i, r := range s.Results {
			vals = append(vals, e.conv(e.expr(r), e.typeOf(r), res.At(i).Type()))
		}
	}
	if len(e.frames) == 0 { // results leave the function being proved
		e.escapeReachable(vals...)
		vals = e.resolve(vals)
	}
	for i, r := range e.named { // a return assigns the named results first
		e.writeVar(r, vals[i])
	}
	e.runDefers()
	if len(e.named) > 0 { // deferred calls may have changed them
		for i, r := range e.named {
			vals[i] = e.readVar(r)
		}
	}
	e.leave(exit{kind: token.RETURN, vals: vals})
}

// leave records an exit under the current guard; control does not continue.
func (e *evaluator) leave(x exit) {
	x.guard, x.st, x.env = e.g, e.st, e.cloneEnv()
	e.exits = append(e.exits, x)
	e.g = e.in.F
}

func (e *evaluator) branchStmt(s *ast.BranchStmt) {
	switch s.Tok {
	case token.BREAK:
		var target ast.Stmt
		if s.Label != nil {
			target = e.labelled(s.Label)
		} else if len(e.outer) > 0 {
			target = e.outer[len(e.outer)-1]
		}
		if target == nil {
			refuse("break to an unknown statement at %s", e.pos(s))
		}
		e.leave(exit{kind: token.BREAK, target: target})
	case token.CONTINUE:
		var target ast.Stmt
		if s.Label != nil {
			target = e.labelled(s.Label)
		} else {
			for i := len(e.outer) - 1; i >= 0; i-- {
				switch e.outer[i].(type) {
				case *ast.ForStmt, *ast.RangeStmt:
					target = e.outer[i]
				}
				if target != nil {
					break
				}
			}
		}
		if target == nil {
			refuse("continue to an unknown loop at %s", e.pos(s))
		}
		e.leave(exit{kind: token.CONTINUE, target: target})
	case token.FALLTHROUGH:
		// handled by the switch, which reads the clause's last statement
	case token.GOTO:
		if loop, ok := e.gotoLoops[s.Label.Name]; ok {
			e.leave(exit{kind: token.CONTINUE, target: loop}) // back to the label: the loop again
			return
		}
		e.forwardGoto(s)
	default:
		refuse("%s is not modelled yet (%s)", s.Tok, e.pos(s))
	}
}

func (e *evaluator) labelled(id *ast.Ident) ast.Stmt {
	for l, s := range e.label {
		if l.(*ast.LabeledStmt).Label.Name == id.Name {
			return s
		}
	}
	return nil
}

func (e *evaluator) cloneEnv() map[types.Object]Node {
	c := make(map[types.Object]Node, len(e.env))
	for k, v := range e.env {
		c[k] = v
	}
	return c
}

// restrictToGuard simplifies the environment and state for where the path
// guard holds.
func (e *evaluator) restrictToGuard() {
	for _, k := range sortedObjs(e.env) {
		e.env[k] = e.in.under(e.env[k], e.g)
	}
	e.st = e.in.under(e.st, e.g)
}

// branch runs then under c and els under !c and merges what falls through.
func (e *evaluator) branch(c Node, then, els func()) {
	g0, env0, st0 := e.g, e.cloneEnv(), e.st
	run := func(guard Node, f func()) part {
		e.g, e.env, e.st = guard, make(map[types.Object]Node, len(env0)), st0
		for k, v := range env0 {
			e.env[k] = v
		}
		if guard != e.in.F {
			e.restrictToGuard()
			f()
		}
		return part{e.g, e.st, e.env}
	}
	t := run(e.in.and(g0, c), then)
	f := run(e.in.and(g0, e.in.not(c)), els)
	e.merge([]part{t, f}, env0)
}

type part = struct {
	g, st Node
	env   map[types.Object]Node
}

// merge joins the ways control reaches one point. Only variables that
// existed before the fork survive it.
func (e *evaluator) merge(parts []part, before map[types.Object]Node) {
	live := parts[:0:0]
	for _, p := range parts {
		if p.g != e.in.F {
			live = append(live, p)
		}
	}
	e.flushDisagreeing(live)
	e.env = map[types.Object]Node{}
	if len(live) == 0 {
		e.g, e.st = e.in.F, parts[0].st
		for k, v := range before {
			e.env[k] = v
		}
		return
	}
	g := e.in.F
	for _, p := range live {
		g = e.in.or(g, p.g)
	}
	pick := func(get func(part) Node) Node {
		acc := get(live[len(live)-1])
		for i := len(live) - 2; i >= 0; i-- {
			acc = e.in.ite(live[i].g, get(live[i]), acc)
		}
		return e.in.under(acc, g)
	}
	for _, k := range sortedObjs(before) {
		present := true
		for _, p := range live {
			if _, ok := p.env[k]; !ok {
				present = false
			}
		}
		if present {
			e.env[k] = pick(func(p part) Node { return p.env[k] })
		}
	}
	e.st = pick(func(p part) Node { return p.st })
	e.g = g
}

func sortedObjs(m map[types.Object]Node) []types.Object {
	out := make([]types.Object, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Pos() < out[j].Pos() || out[i].Pos() == out[j].Pos() && out[i].Name() < out[j].Name()
	})
	return out
}

// switchStmt: the tag once, then the cases in order (default last), each
// case's expressions in order; fallthrough continues into the next body;
// break leaves the switch.
func (e *evaluator) switchStmt(s *ast.SwitchStmt) {
	if s.Init != nil {
		e.stmt(s.Init)
	}
	var tag Node = -1
	var tagT types.Type
	if s.Tag != nil {
		tag, tagT = e.value(s.Tag), e.typeOf(s.Tag)
	}
	clauses := make([]*ast.CaseClause, len(s.Body.List))
	var order []int
	def := -1
	for i, c := range s.Body.List {
		clauses[i] = c.(*ast.CaseClause)
		if clauses[i].List == nil {
			def = i
		} else {
			order = append(order, i)
		}
	}
	if def >= 0 {
		order = append(order, def)
	}
	var runBody func(i int)
	runBody = func(i int) {
		body := clauses[i].Body
		if n := len(body); n > 0 {
			if b, ok := body[n-1].(*ast.BranchStmt); ok && b.Tok == token.FALLTHROUGH {
				e.block(body[:n-1])
				if e.g != e.in.F {
					runBody(i + 1)
				}
				return
			}
		}
		e.block(body)
	}
	var try func(k int)
	try = func(k int) {
		if k == len(order) {
			return
		}
		i := order[k]
		if i == def {
			runBody(i)
			return
		}
		c := e.caseCond(clauses[i].List, func(x ast.Expr) Node {
			if tag < 0 {
				return e.condOf(x)
			}
			return e.compare(token.EQL, tag, e.value(x), tagT, e.typeOf(x), x)
		})
		e.branch(c, func() { runBody(i) }, func() { try(k + 1) })
	}
	e.breakable(s, func() { try(0) })
}

// caseCond: e1 || e2 || ..., evaluated lazily in order.
func (e *evaluator) caseCond(list []ast.Expr, test func(ast.Expr) Node) Node {
	c := test(list[0])
	for _, x := range list[1:] {
		c = e.orElse(c, func() Node { return test(x) })
	}
	return c
}

// breakable runs a switch (or loop) body and merges the breaks leaving it
// with what falls out of it.
func (e *evaluator) breakable(s ast.Stmt, run func()) {
	before := e.cloneEnv()
	outer := len(e.exits)
	e.outer = append(e.outer, s)
	run()
	e.outer = e.outer[:len(e.outer)-1]
	var mine, rest []exit
	for i, x := range e.exits {
		if i >= outer && x.kind == token.BREAK && x.target == s {
			mine = append(mine, x)
		} else {
			rest = append(rest, x)
		}
	}
	if len(mine) == 0 {
		return
	}
	e.exits = rest
	parts := []part{{e.g, e.st, e.env}}
	for _, x := range mine {
		parts = append(parts, part{x.guard, x.st, x.env})
	}
	e.merge(parts, before)
}

func (e *evaluator) typeSwitch(s *ast.TypeSwitchStmt) {
	if s.Init != nil {
		e.stmt(s.Init)
	}
	var x ast.Expr
	switch a := s.Assign.(type) {
	case *ast.AssignStmt:
		x = a.Rhs[0].(*ast.TypeAssertExpr).X
	case *ast.ExprStmt:
		x = a.X.(*ast.TypeAssertExpr).X
	}
	v, vt := e.value(x), e.typeOf(x)
	var order []int
	def := -1
	clauses := make([]*ast.CaseClause, len(s.Body.List))
	for i, c := range s.Body.List {
		clauses[i] = c.(*ast.CaseClause)
		if clauses[i].List == nil {
			def = i
		} else {
			order = append(order, i)
		}
	}
	if def >= 0 {
		order = append(order, def)
	}
	body := func(i int) {
		cl := clauses[i]
		if obj := e.s.Info.Implicits[cl]; obj != nil {
			ot := obj.Type()
			if types.IsInterface(ot) {
				e.declare(obj, e.conv(v, vt, ot))
			} else {
				e.declare(obj, e.pure("assertval", ot, v))
			}
		}
		e.block(cl.Body)
	}
	var try func(k int)
	try = func(k int) {
		if k == len(order) {
			return
		}
		i := order[k]
		if i == def {
			body(i)
			return
		}
		c := e.caseCond(clauses[i].List, func(t ast.Expr) Node {
			if id, ok := t.(*ast.Ident); ok && id.Name == "nil" && e.s.Info.Uses[id] == types.Universe.Lookup("nil") {
				return e.compare(token.EQL, v, e.zero(vt), vt, vt, t)
			}
			return e.in.atom(e.in.mk(opPure, "bool", "isa:"+e.key(e.typeOf(t)), v))
		})
		e.branch(c, func() { body(i) }, func() { try(k + 1) })
	}
	e.breakable(s, func() { try(0) })
}

// ---------------------------------------------------------------------------
// assignment

func (e *evaluator) assign(s *ast.AssignStmt) {
	if s.Tok != token.ASSIGN && s.Tok != token.DEFINE {
		get, set := e.lvalue(s.Lhs[0])
		op := map[token.Token]token.Token{
			token.ADD_ASSIGN: token.ADD, token.SUB_ASSIGN: token.SUB, token.MUL_ASSIGN: token.MUL,
			token.QUO_ASSIGN: token.QUO, token.REM_ASSIGN: token.REM, token.AND_ASSIGN: token.AND,
			token.OR_ASSIGN: token.OR, token.XOR_ASSIGN: token.XOR, token.SHL_ASSIGN: token.SHL,
			token.SHR_ASSIGN: token.SHR, token.AND_NOT_ASSIGN: token.AND_NOT,
		}[s.Tok]
		lt, rt := e.typeOf(s.Lhs[0]), e.typeOf(s.Rhs[0])
		set(e.binary(op, get(), e.expr(s.Rhs[0]), lt, rt, lt, s))
		return
	}
	// the operands of the left-hand side, then the right-hand side, then the
	// assignments in order
	sets := make([]func(Node), len(s.Lhs))
	for i, l := range s.Lhs {
		if s.Tok == token.DEFINE {
			id := l.(*ast.Ident)
			if obj := e.s.Info.Defs[id]; obj != nil {
				sets[i] = func(v Node) { e.declare(obj, v) }
				continue
			}
			if obj := e.s.Info.Uses[id]; obj != nil { // redeclared: an assignment
				sets[i] = func(v Node) { e.writeVar(obj, v) }
				continue
			}
			sets[i] = func(Node) {}
			continue
		}
		_, sets[i] = e.lvalue(l)
	}
	var vals []Node
	var ts []types.Type
	if len(s.Rhs) == 1 && len(s.Lhs) > 1 {
		vals, ts = e.exprsTyped(s.Rhs[0])
	} else {
		for _, r := range s.Rhs {
			vals = append(vals, e.expr(r))
			ts = append(ts, e.typeOf(r))
		}
	}
	for i, l := range s.Lhs {
		var lt types.Type
		if id, ok := l.(*ast.Ident); ok && id.Name == "_" {
			continue
		}
		lt = e.typeOf(l)
		sets[i](e.conv(vals[i], ts[i], lt))
	}
}

// lvalue evaluates the operands of an assignable expression now and returns
// how to read and write it later.
func (e *evaluator) lvalue(x ast.Expr) (get func() Node, set func(Node)) {
	switch x := x.(type) {
	case *ast.ParenExpr:
		return e.lvalue(x.X)
	case *ast.Ident:
		if x.Name == "_" {
			return func() Node { return -1 }, func(Node) {}
		}
		obj := e.s.Info.Uses[x]
		if obj == nil {
			obj = e.s.Info.Defs[x]
		}
		v, ok := obj.(*types.Var)
		if !ok {
			refuse("assignment to %s at %s", x.Name, e.pos(x))
		}
		if e.isGlobal(v) {
			k := objKey(v)
			return func() Node { return e.effect("load:"+k, []types.Type{v.Type()})[0] },
				func(n Node) { e.effect("store:"+k, nil, n) }
		}
		if _, ok := e.env[v]; !ok {
			refuse("%s is not in scope at %s", v.Name(), e.pos(x))
		}
		return func() Node { return e.readVar(v) }, func(n Node) { e.writeVar(v, n) }
	case *ast.SelectorExpr:
		sel := e.s.Info.Selections[x]
		if sel == nil { // pkg.Var
			return e.lvalue(x.Sel)
		}
		if sel.Kind() != types.FieldVal {
			refuse("assignment to a method at %s", e.pos(x))
		}
		ft := sel.Obj().Type()
		path := sel.Index()
		if sel.Indirect() {
			addr := e.fieldAddr(e.expr(x.X), e.typeOf(x.X), path)
			return func() Node { return e.effect("load", []types.Type{ft}, addr)[0] },
				func(n Node) { e.effect("store", nil, addr, n) }
		}
		// a field of a struct value held in a variable: functional update
		get0, set0 := e.lvalue(x.X)
		idx := path[len(path)-1]
		return func() Node { return e.pure("field:"+strconv.Itoa(idx), ft, get0()) },
			func(n Node) {
				set0(e.in.lift([]Node{get0(), n}, func(l []Node) Node {
					return e.in.mk(opPure, e.key(e.typeOf(x.X)), "with:"+strconv.Itoa(idx), l...)
				}))
			}
	case *ast.IndexExpr:
		xt := e.typeOf(x.X)
		et := e.typeOf(x)
		switch u := xt.Underlying().(type) {
		case *types.Map:
			m, k := e.expr(x.X), e.conv(e.expr(x.Index), e.typeOf(x.Index), u.Key())
			return func() Node { return e.effect("mapget", []types.Type{et}, m, k)[0] },
				func(n Node) { e.effect("mapset", nil, m, k, n) }
		case *types.Array: // an array value in a variable
			get0, set0 := e.lvalue(x.X)
			i := e.expr(x.Index)
			return func() Node { return e.effect("index", []types.Type{et}, get0(), i)[0] },
				func(n Node) { set0(e.effect("arrayset", []types.Type{xt}, get0(), i, n)[0]) }
		default: // slice, pointer to array
			base, i := e.expr(x.X), e.expr(x.Index)
			return func() Node { return e.effect("index", []types.Type{et}, base, i)[0] },
				func(n Node) { e.effect("setindex", nil, base, i, n) }
		}
	case *ast.StarExpr:
		p := e.expr(x.X)
		t := e.typeOf(x)
		return func() Node { return e.effect("load", []types.Type{t}, p)[0] },
			func(n Node) { e.effect("store", nil, p, n) }
	}
	refuse("cannot assign to %T at %s", x, e.pos(x))
	return nil, nil
}

func (e *evaluator) isGlobal(v *types.Var) bool {
	return v.Pkg() != nil && v.Parent() == v.Pkg().Scope()
}

// fieldAddr: the address of a (promoted) field reached through a pointer.
// Computing it cannot fail; using it can (nil).
func (e *evaluator) fieldAddr(base Node, t types.Type, path []int) Node {
	addr := base
	for i, idx := range path {
		if i > 0 && !isPointer(t) {
			// an embedded struct value inside what the pointer points to
		} else if i > 0 {
			addr = e.effect("load", []types.Type{t}, addr)[0] // an embedded pointer: dereference it
		}
		st := derefStruct(t)
		f := st.Field(idx)
		addr = e.in.mk(opPure, "addr", "fieldaddr:"+strconv.Itoa(idx), addr)
		t = f.Type()
	}
	return addr
}

func isPointer(t types.Type) bool { _, ok := t.Underlying().(*types.Pointer); return ok }

func derefStruct(t types.Type) *types.Struct {
	if p, ok := t.Underlying().(*types.Pointer); ok {
		t = p.Elem()
	}
	s, ok := t.Underlying().(*types.Struct)
	if !ok {
		refuse("a field of a non-struct type %s", t)
	}
	return s
}

// ---------------------------------------------------------------------------
// expressions

func (e *evaluator) expr(x ast.Expr) Node {
	vs := e.exprs(x)
	if len(vs) != 1 {
		refuse("expected one value at %s", e.pos(x))
	}
	return vs[0]
}

// exprsTyped evaluates x where several values are expected: a call's
// results, or the two values of a comma-ok form.
func (e *evaluator) exprsTyped(x ast.Expr) ([]Node, []types.Type) {
	if vs, ok := e.commaOK(x); ok {
		return vs, []types.Type{e.typeOf(x), types.Typ[types.Bool]}
	}
	vs := e.exprs(x)
	t := e.typeOf(x)
	if tup, ok := t.(*types.Tuple); ok {
		ts := make([]types.Type, tup.Len())
		for i := range ts {
			ts[i] = tup.At(i).Type()
		}
		return vs, ts
	}
	if len(vs) == 2 { // comma-ok forms
		return vs, []types.Type{t, types.Typ[types.Bool]}
	}
	return vs, []types.Type{t}
}

// exprs evaluates x to its values: one, or several for a call or a comma-ok
// form used where two values are expected (the caller asks via commaOK).
func (e *evaluator) exprs(x ast.Expr) []Node {
	if tv, ok := e.s.Info.Types[x]; ok && tv.Value != nil {
		return []Node{e.constant(tv)}
	}
	switch x := x.(type) {
	case *ast.ParenExpr:
		return e.exprs(x.X)
	case *ast.Ident:
		return []Node{e.ident(x)}
	case *ast.BasicLit:
		refuse("a literal without a constant value at %s", e.pos(x))
	case *ast.SelectorExpr:
		return []Node{e.selector(x)}
	case *ast.CallExpr:
		return e.call(x)
	case *ast.StarExpr:
		// *new(T) is the zero value of T: new cannot fail, and an allocation
		// nothing else refers to cannot be observed
		if c, ok := ast.Unparen(x.X).(*ast.CallExpr); ok {
			if b, ok := e.s.Info.Uses[identOf(c.Fun)].(*types.Builtin); ok && b.Name() == "new" {
				return []Node{e.zero(e.typeOf(x))}
			}
		}
		return e.effect("load", []types.Type{e.typeOf(x)}, e.expr(x.X))
	case *ast.UnaryExpr:
		return e.unary(x)
	case *ast.BinaryExpr:
		lt, rt := e.typeOf(x.X), e.typeOf(x.Y)
		switch x.Op {
		case token.LAND, token.LOR:
			a := e.cond(x.X)
			if x.Op == token.LAND {
				return []Node{e.andThen(a, func() Node { return e.cond(x.Y) })}
			}
			return []Node{e.orElse(a, func() Node { return e.cond(x.Y) })}
		case token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ:
			if r, ok := e.ruleCompare(x); ok {
				return []Node{r}
			}
			return []Node{e.compare(x.Op, e.expr(x.X), e.expr(x.Y), lt, rt, x)}
		}
		return []Node{e.binary(x.Op, e.expr(x.X), e.expr(x.Y), lt, rt, e.typeOf(x), x)}
	case *ast.IndexExpr:
		return e.index(x)
	case *ast.IndexListExpr:
		refuse("generic instantiation is not modelled yet (%s)", e.pos(x))
	case *ast.SliceExpr:
		var ops []Node
		if _, ok := e.typeOf(x.X).Underlying().(*types.Array); ok {
			ops = []Node{e.addressOf(x.X)} // slicing an array variable takes its address
		} else {
			ops = []Node{e.expr(x.X)}
		}
		for _, b := range []ast.Expr{x.Low, x.High, x.Max} {
			if b == nil {
				ops = append(ops, e.in.mk(opConst, "none", ""))
			} else {
				ops = append(ops, e.expr(b))
			}
		}
		// s[:len(s)] is s: the same string or slice header, and it cannot panic
		if x.Low == nil && x.Max == nil && x.High != nil && !x.Slice3 {
			if lt := e.in.t(ops[2]); lt.op == opPure && lt.aux == "len" && len(lt.kids) == 1 && lt.kids[0] == ops[0] {
				return []Node{ops[0]}
			}
		}
		return e.effect("slice", []types.Type{e.typeOf(x)}, ops...)
	case *ast.TypeAssertExpr:
		t := e.typeOf(x)
		return e.effect("assert:"+e.key(t), []types.Type{t}, e.expr(x.X))
	case *ast.CompositeLit:
		return []Node{e.composite(x)}
	case *ast.FuncLit:
		return []Node{e.funcLit(x)}
	case *ast.KeyValueExpr:
		refuse("a key-value outside a composite literal at %s", e.pos(x))
	}
	refuse("%T is not modelled (%s)", x, e.pos(x))
	return nil
}

// commaOK evaluates the two-value forms v, ok := m[k] / x.(T) / <-ch.
func (e *evaluator) commaOK(x ast.Expr) ([]Node, bool) {
	switch x := ast.Unparen(x).(type) {
	case *ast.IndexExpr:
		if m, ok := e.typeOf(x.X).Underlying().(*types.Map); ok {
			return e.effect("mapget2", []types.Type{m.Elem(), types.Typ[types.Bool]}, e.expr(x.X), e.conv(e.expr(x.Index), e.typeOf(x.Index), m.Key())), true
		}
	case *ast.TypeAssertExpr:
		t := e.typeOf(x)
		v := e.expr(x.X)
		return []Node{e.pure("assertok:"+e.key(t), t, v), e.in.atom(e.in.mk(opPure, "bool", "isa:"+e.key(t), v))}, true
	case *ast.UnaryExpr:
		if x.Op == token.ARROW {
			elem := e.typeOf(x.X).Underlying().(*types.Chan).Elem()
			return e.effect("recv2", []types.Type{elem, types.Typ[types.Bool]}, e.expr(x.X)), true
		}
	}
	return nil, false
}

func (e *evaluator) ident(x *ast.Ident) Node {
	obj := e.s.Info.Uses[x]
	if obj == nil {
		obj = e.s.Info.Defs[x]
	}
	switch o := obj.(type) {
	case *types.Var:
		if e.isGlobal(o) {
			return e.effect("load:"+objKey(o), []types.Type{o.Type()})[0]
		}
		if _, ok := e.env[o]; !ok {
			refuse("%s is not in scope at %s", o.Name(), e.pos(x))
		}
		return e.readVar(o)
	case *types.Nil:
		return e.in.mk(opZero, "untyped nil", "")
	case *types.Func:
		k := objKey(o)
		if e.inline[k] != nil {
			refuse("%s, which the change added or removed, is used as a value at %s", k, e.pos(x))
		}
		return e.in.mk(opFunc, e.key(o.Type()), k)
	}
	refuse("identifier %s (%T) at %s", x.Name, obj, e.pos(x))
	return -1
}

func (e *evaluator) selector(x *ast.SelectorExpr) Node {
	sel := e.s.Info.Selections[x]
	if sel == nil { // a qualified identifier: pkg.Name
		return e.ident(x.Sel)
	}
	switch sel.Kind() {
	case types.FieldVal:
		base, t := e.expr(x.X), e.typeOf(x.X)
		if sel.Indirect() {
			addr := e.fieldAddr(base, t, sel.Index())
			return e.effect("load", []types.Type{sel.Obj().Type()}, addr)[0]
		}
		for _, idx := range sel.Index() {
			f := derefStruct(t).Field(idx)
			base = e.pure("field:"+strconv.Itoa(idx), f.Type(), base)
			t = f.Type()
		}
		return base
	}
	if sel.Kind() == types.MethodExpr {
		return e.in.mk(opFunc, e.key(e.typeOf(x)), objKey(sel.Obj()))
	}
	recv := e.receiver(x, sel)[0] // a method value binds its receiver now
	if types.IsInterface(sel.Recv()) {
		return e.in.mk(opPure, e.key(e.typeOf(x)), "ibound:"+sel.Obj().Name(), recv)
	}
	return e.in.mk(opPure, e.key(e.typeOf(x)), "bound:"+objKey(sel.Obj()), recv)
}

func (e *evaluator) unary(x *ast.UnaryExpr) []Node {
	t := e.typeOf(x)
	switch x.Op {
	case token.NOT:
		return []Node{e.in.not(e.cond(x.X))}
	case token.ARROW:
		return e.effect("recv", []types.Type{t}, e.expr(x.X))
	case token.AND:
		return []Node{e.addressOf(x.X)}
	case token.SUB, token.ADD, token.XOR:
		return []Node{e.pure("unary:"+x.Op.String(), t, e.expr(x.X))}
	}
	refuse("unary %s at %s", x.Op, e.pos(x))
	return nil
}

// cond evaluates a boolean expression to its BDD.
func (e *evaluator) cond(x ast.Expr) Node { return e.in.atom(e.expr(x)) }

// andThen / orElse: short-circuit, with b evaluated (and its effects run)
// only where a allows.
func (e *evaluator) andThen(a Node, b func() Node) Node { return e.shortCircuit(a, true, b) }
func (e *evaluator) orElse(a Node, b func() Node) Node  { return e.shortCircuit(a, false, b) }

func (e *evaluator) shortCircuit(a Node, and bool, b func() Node) Node {
	g0, st0 := e.g, e.st
	env0 := e.cloneEnv()
	when := a
	if !and {
		when = e.in.not(a)
	}
	e.g = e.in.and(g0, when)
	var bv Node = e.in.F
	stB := st0
	if e.g != e.in.F {
		e.restrictToGuard()
		bv = b()
		stB = e.st
	}
	e.g, e.env = g0, env0
	e.st = e.in.under(e.in.ite(when, stB, st0), g0)
	if and {
		return e.in.ite(a, bv, e.in.F)
	}
	return e.in.ite(a, e.in.T, bv)
}

// compare: comparisons are pure unless they compare interfaces, which panics
// on uncomparable dynamic types.
func (e *evaluator) compare(op token.Token, a, b Node, at, bt types.Type, at_ ast.Node) Node {
	// operands are converted to a common type first
	t := at
	if b, ok := at.(*types.Basic); ok && b.Info()&types.IsUntyped != 0 {
		t = bt
	}
	a, b = e.conv(a, at, t), e.conv(b, bt, t)
	if op == token.NEQ {
		return e.in.not(e.compare(token.EQL, a, b, t, t, at_))
	}
	// Comparing two interface values panics when their dynamic types match and
	// are not comparable; against nil or a value of a comparable concrete type
	// it cannot.
	if types.IsInterface(at) && types.IsInterface(bt) && !e.isNil(a) && !e.isNil(b) {
		return e.effect("cmp"+op.String(), []types.Type{types.Typ[types.Bool]}, a, b)[0]
	}
	if isBool(t) && op == token.EQL { // a == b on booleans is a BDD operation
		return e.in.ite(a, b, e.in.not(b))
	}
	return e.in.atom(e.in.lift([]Node{a, b}, func(l []Node) Node {
		if r, ok := e.foldCompare(op, l[0], l[1]); ok {
			return r
		}
		if op == token.EQL && e.in.order(l[0], l[1]) > 0 { // == is symmetric
			l[0], l[1] = l[1], l[0]
		}
		return e.in.atom(e.in.mk(opPure, "bool", "cmp"+op.String(), l...))
	}))
}

// isNil: the zero value of an interface-typed operand is nil.
func (e *evaluator) isNil(n Node) bool { return e.in.t(n).op == opZero }

// foldCompare decides a comparison of two constants of the same type.
func (e *evaluator) foldCompare(op token.Token, a, b Node) (Node, bool) {
	ta, tb := e.in.t(a), e.in.t(b)
	if ta.op == opZero && tb.op == opZero && ta.typ == tb.typ && op == token.EQL {
		return e.in.T, true
	}
	if ta.op != opConst || tb.op != opConst || ta.typ != tb.typ {
		return -1, false
	}
	va, vb := constant.MakeFromLiteral(ta.aux, token.INT, 0), constant.MakeFromLiteral(tb.aux, token.INT, 0)
	if va.Kind() == constant.Unknown || vb.Kind() == constant.Unknown {
		if op == token.EQL {
			if ta.aux == tb.aux {
				return e.in.T, true
			}
			if ta.aux[0] == '"' && tb.aux[0] == '"' {
				return e.in.F, true
			}
		}
		return -1, false
	}
	if constant.Compare(va, op, vb) {
		return e.in.T, true
	}
	return e.in.F, true
}

// binary: arithmetic and friends. Division and shifts may panic, so they are
// effects unless the right operand makes that impossible.
func (e *evaluator) binary(op token.Token, a, b Node, at, bt, rt types.Type, at_ ast.Node) Node {
	if op != token.SHL && op != token.SHR {
		a, b = e.conv(a, at, rt), e.conv(b, bt, rt)
	}
	name := "binary:" + op.String()
	switch op {
	case token.QUO, token.REM:
		if isInteger(rt) && !e.safeDivisor(b) {
			return e.effect(name, []types.Type{rt}, a, b)[0]
		}
	case token.SHL, token.SHR:
		if !e.nonNegativeConst(b) {
			return e.effect(name, []types.Type{rt}, a, b)[0]
		}
	}
	return e.pure(name, rt, a, b)
}

func isInteger(t types.Type) bool {
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Info()&types.IsInteger != 0
}

func (e *evaluator) safeDivisor(b Node) bool {
	t := e.in.t(b)
	if t.op != opConst {
		return false
	}
	v := constant.MakeFromLiteral(t.aux, token.INT, 0)
	return v.Kind() == constant.Int && constant.Sign(v) != 0 && constant.Compare(v, token.NEQ, constant.MakeInt64(-1))
}

func (e *evaluator) nonNegativeConst(b Node) bool {
	t := e.in.t(b)
	if t.op != opConst {
		return false
	}
	v := constant.MakeFromLiteral(t.aux, token.INT, 0)
	return v.Kind() == constant.Int && constant.Sign(v) >= 0
}

func (e *evaluator) index(x *ast.IndexExpr) []Node {
	if tv, ok := e.s.Info.Types[x.X]; ok && tv.IsType() {
		refuse("generic instantiation is not modelled yet (%s)", e.pos(x))
	}
	if _, ok := e.s.Info.Instances[identOf(x.X)]; ok {
		refuse("generic instantiation is not modelled yet (%s)", e.pos(x))
	}
	t := e.typeOf(x)
	switch u := e.typeOf(x.X).Underlying().(type) {
	case *types.Map:
		return e.effect("mapget", []types.Type{t}, e.expr(x.X), e.conv(e.expr(x.Index), e.typeOf(x.Index), u.Key()))
	}
	return e.effect("index", []types.Type{t}, e.expr(x.X), e.expr(x.Index))
}

func identOf(x ast.Expr) *ast.Ident {
	switch x := ast.Unparen(x).(type) {
	case *ast.Ident:
		return x
	case *ast.SelectorExpr:
		return x.Sel
	}
	return nil
}

// composite: struct and array values are pure; slices and maps allocate.
func (e *evaluator) composite(x *ast.CompositeLit) Node {
	t := e.typeOf(x)
	switch u := t.Underlying().(type) {
	case *types.Struct:
		fields := make([]Node, u.NumFields())
		set := make([]bool, u.NumFields())
		for i, el := range x.Elts {
			idx, val := i, el
			if kv, ok := el.(*ast.KeyValueExpr); ok {
				name := kv.Key.(*ast.Ident).Name
				for j := 0; j < u.NumFields(); j++ {
					if u.Field(j).Name() == name {
						idx = j
					}
				}
				val = kv.Value
			}
			fields[idx] = e.conv(e.elem(val, u.Field(idx).Type()), e.typeOf(val), u.Field(idx).Type())
			set[idx] = true
		}
		for i := range fields {
			if !set[i] {
				fields[i] = e.zero(u.Field(i).Type())
			}
		}
		return e.in.lift(fields, func(l []Node) Node { return e.in.mk(opPure, e.key(t), "struct", l...) })
	case *types.Array, *types.Slice, *types.Map:
		var elemT, keyT types.Type
		switch u := u.(type) {
		case *types.Array:
			elemT = u.Elem()
		case *types.Slice:
			elemT = u.Elem()
		case *types.Map:
			elemT, keyT = u.Elem(), u.Key()
		}
		var ops []Node
		for _, el := range x.Elts {
			if kv, ok := el.(*ast.KeyValueExpr); ok {
				kt := keyT
				if kt == nil {
					kt = types.Typ[types.Int]
				}
				ops = append(ops, e.conv(e.elem(kv.Key, kt), e.typeOf(kv.Key), kt), e.conv(e.elem(kv.Value, elemT), e.typeOf(kv.Value), elemT))
			} else {
				ops = append(ops, e.in.mk(opConst, "none", ""), e.conv(e.elem(el, elemT), e.typeOf(el), elemT))
			}
		}
		if _, isArray := u.(*types.Array); isArray {
			return e.in.lift(ops, func(l []Node) Node { return e.in.mk(opPure, e.key(t), "array", l...) })
		}
		return e.effect("alloc:"+e.key(t), []types.Type{t}, ops...)[0]
	}
	refuse("a composite literal of type %s at %s", t, e.pos(x))
	return -1
}

// elem evaluates a composite element, whose literal type may be elided.
func (e *evaluator) elem(x ast.Expr, t types.Type) Node {
	if cl, ok := x.(*ast.CompositeLit); ok && cl.Type == nil {
		if e.s.Info.Types[cl].Type == nil {
			refuse("an elided composite literal without a type at %s", e.pos(x))
		}
	}
	return e.expr(x)
}

// ---------------------------------------------------------------------------
// calls

func (e *evaluator) call(x *ast.CallExpr) []Node {
	fun := ast.Unparen(x.Fun)
	if tv := e.s.Info.Types[fun]; tv.IsType() {
		return []Node{e.conversion(x, tv.Type)}
	}
	if r, ok := e.ruleCall(x); ok {
		return r
	}
	if name := funcName(funcObj(e.s, fun)); pureFuncTerms[name] && !x.Ellipsis.IsValid() {
		sig := e.typeOf(fun).(*types.Signature)
		if sig.Results().Len() == 1 {
			return []Node{e.pure("call:"+name, sig.Results().At(0).Type(), e.args(x, sig)...)}
		}
	}
	if id := identOf(fun); id != nil {
		if b, ok := e.s.Info.Uses[id].(*types.Builtin); ok {
			return e.builtin(b.Name(), x)
		}
	}
	sig, ok := e.typeOf(fun).Underlying().(*types.Signature)
	if !ok {
		refuse("a call of a non-function at %s", e.pos(x))
	}
	var results []types.Type
	for i := 0; i < sig.Results().Len(); i++ {
		results = append(results, sig.Results().At(i).Type())
	}
	// the callee
	var name string
	var recv []Node
	switch f := fun.(type) {
	case *ast.Ident, *ast.SelectorExpr:
		var obj types.Object
		if sel, ok := f.(*ast.SelectorExpr); ok {
			if s := e.s.Info.Selections[sel]; s != nil {
				switch s.Kind() {
				case types.MethodVal:
					recv, name = e.receiver(sel, s), "call:"+objKey(s.Obj())
					if types.IsInterface(s.Recv()) {
						name = "icall:" + s.Obj().Name()
					}
				case types.MethodExpr:
					name = "call:" + objKey(s.Obj())
				default: // a field of function type
					recv, name = []Node{e.expr(f)}, "callv"
				}
			} else {
				obj = e.s.Info.Uses[sel.Sel]
			}
		} else {
			obj = e.s.Info.Uses[f.(*ast.Ident)]
		}
		if obj != nil {
			switch o := obj.(type) {
			case *types.Func:
				name = "call:" + objKey(o)
				if e.inline[objKey(o)] != nil {
					return e.inlineCall(objKey(o), e.args(x, sig))
				}
			default: // a variable of function type
				recv, name = []Node{e.expr(f)}, "callv"
			}
		}
	case *ast.FuncLit:
		return e.callLiteral(f, x)
	default:
		recv, name = []Node{e.expr(f)}, "callv"
	}
	args := e.args(x, sig)
	return e.effect(name, results, append(recv, args...)...)
}

// receiver evaluates a method call's receiver, following embedded fields and
// taking or dropping the address the method needs.
func (e *evaluator) receiver(x *ast.SelectorExpr, s *types.Selection) []Node {
	base, t := e.expr(x.X), e.typeOf(x.X)
	path := s.Index()
	for _, idx := range path[:len(path)-1] { // embedded fields on the way
		f := derefStruct(t).Field(idx)
		if isPointer(t) {
			base = e.effect("load", []types.Type{f.Type()}, e.fieldAddr(base, t, []int{idx}))[0]
		} else {
			base = e.pure("field:"+strconv.Itoa(idx), f.Type(), base)
		}
		t = f.Type()
	}
	if types.IsInterface(t) {
		return []Node{base}
	}
	sig := s.Obj().Type().(*types.Signature)
	wantPtr := isPointer(sig.Recv().Type())
	switch {
	case wantPtr && !isPointer(t):
		// v.M() with M on *T: the address of v (a cell) or of the field
		xt := e.typeOf(x.X)
		if isPointer(xt) {
			return []Node{e.fieldAddr(e.expr(x.X), xt, path[:len(path)-1])}
		}
		return []Node{e.fieldAddr(e.addressOf(x.X), types.NewPointer(xt), path[:len(path)-1])}
	case !wantPtr && isPointer(t):
		base = e.effect("load", []types.Type{t.Underlying().(*types.Pointer).Elem()}, base)[0]
	}
	return []Node{base}
}

// args evaluates a call's arguments, converts them to the parameter types
// and packs variadic ones into a slice.
func (e *evaluator) args(x *ast.CallExpr, sig *types.Signature) []Node {
	var vals []Node
	var ts []types.Type
	if len(x.Args) == 1 && sig.Params().Len() > 1 {
		vals, ts = e.exprsTyped(x.Args[0])
	} else {
		for _, a := range x.Args {
			vals = append(vals, e.expr(a))
			ts = append(ts, e.typeOf(a))
		}
	}
	params := sig.Params()
	n := params.Len()
	var out []Node
	for i := 0; i < len(vals); i++ {
		if sig.Variadic() && i >= n-1 && !x.Ellipsis.IsValid() {
			elemT := params.At(n - 1).Type().(*types.Slice).Elem()
			var elems []Node
			for j := i; j < len(vals); j++ {
				elems = append(elems, e.in.mk(opConst, "none", ""), e.conv(vals[j], ts[j], elemT))
			}
			out = append(out, e.effect("alloc:"+e.key(params.At(n-1).Type()), []types.Type{params.At(n - 1).Type()}, elems...)[0])
			return out
		}
		out = append(out, e.conv(vals[i], ts[i], params.At(i).Type()))
	}
	if sig.Variadic() && len(vals) == n-1 {
		out = append(out, e.zero(params.At(n-1).Type())) // no variadic arguments: a nil slice
	}
	return out
}

// inlineCall evaluates a call to a function the change added or removed in
// place: parameters bound to the arguments, its exits folded into its result.
func (e *evaluator) inlineCall(key string, args []Node) []Node {
	if slices.Contains(e.stack, key) {
		refuse("%s is recursive", key)
	}
	fd := e.inline[key]
	sig := e.s.Info.Defs[fd.Name].Type().(*types.Signature)
	// save the caller's frame
	env, exits, fsig, named, outer, g := e.env, e.exits, e.sig, e.named, e.outer, e.g
	defers, fdepth := e.defers, e.frameDepth
	defer func() { e.defers, e.frameDepth = defers, fdepth }()
	defer e.pushFrame(frameInlined)()
	e.stack = append(e.stack, key)
	root := e.root
	e.root = fd
	e.analyseCells(fd)
	frameCells := e.frameCells
	e.env = map[types.Object]Node{}
	e.frameCells = e.callerCells(env, e.env)
	for i := 0; i < sig.Params().Len(); i++ {
		e.declare(sig.Params().At(i), args[i])
	}
	r := e.body(sig, fd.Body)
	cellOut := e.cellsOut
	e.frameCells, e.cellsOut = frameCells, nil
	defer e.returnedCells(cellOut, g)
	e.root = root
	e.stack = e.stack[:len(e.stack)-1]
	e.env, e.exits, e.sig, e.named, e.outer, e.g = env, exits, fsig, named, outer, g
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

func (e *evaluator) conversion(x *ast.CallExpr, to types.Type) Node {
	arg := x.Args[0]
	from := e.typeOf(arg)
	v := e.expr(arg)
	tu, fu := to.Underlying(), from.Underlying()
	_, toSlice := tu.(*types.Slice)
	_, fromSlice := fu.(*types.Slice)
	isString := func(t types.Type) bool {
		b, ok := t.(*types.Basic)
		return ok && b.Info()&types.IsString != 0
	}
	switch {
	case toSlice && isString(fu), isString(tu) && fromSlice:
		return e.effect("convmem:"+e.key(to), []types.Type{to}, v)[0] // allocates or reads memory
	case fromSlice && !toSlice:
		return e.effect("convarr:"+e.key(to), []types.Type{to}, v)[0] // may panic
	}
	if b, ok := from.(*types.Basic); ok && b.Kind() == types.UntypedNil {
		return e.zero(to)
	}
	fk := e.key(from)
	return e.norm(e.in.lift([]Node{v}, func(l []Node) Node { return e.in.mk(opPure, e.key(to), "conv:"+fk, l[0]) }), to)
}

func (e *evaluator) builtin(name string, x *ast.CallExpr) []Node {
	t := e.typeOf(x)
	var results []types.Type
	if tup, ok := t.(*types.Tuple); ok {
		for i := 0; i < tup.Len(); i++ {
			results = append(results, tup.At(i).Type())
		}
	} else if t != nil {
		results = []types.Type{t}
	}
	var args []Node
	for _, a := range x.Args {
		if tv := e.s.Info.Types[a]; tv.IsType() {
			args = append(args, e.in.mk(opConst, "type", e.key(tv.Type)))
			continue
		}
		args = append(args, e.expr(a))
	}
	switch name {
	case "len", "cap":
		switch e.typeOf(x.Args[0]).Underlying().(type) {
		case *types.Map, *types.Chan, *types.Pointer:
			return e.effect(name, results, args...)
		}
		return []Node{e.pure(name, t, args...)}
	case "min", "max", "complex", "real", "imag":
		for i, a := range x.Args {
			args[i] = e.conv(args[i], e.typeOf(a), t)
		}
		return []Node{e.pure(name, t, args...)}
	case "recover":
		// recover stops a panic only in a function the panicking frame deferred
		// directly; inlining changes which function that is.
		if n := len(e.frames); n > 0 && e.frames[n-1] == frameInlined {
			refuse("recover in an inlined function (%s)", e.pos(x))
		}
		return e.effect("recover", results)
	}
	if x.Ellipsis.IsValid() {
		name += "..."
	}
	return e.effect("builtin:"+name, results, args...)
}

// ---------------------------------------------------------------------------
// evaluation order

// hazard refuses expressions whose meaning depends on an order the language
// leaves unspecified: an operand that calls (or receives) beside another
// operand that reads memory the call may write. Go only orders calls,
// receives and logical operators among themselves; gc reads other operands
// when it likes, so a model that reads them left to right could "prove" a
// change that moves a read across such a call.
func (e *evaluator) hazard(xs ...ast.Expr) bool {
	type summary struct{ calls, reads bool }
	var sum func(n ast.Node) summary
	sum = func(n ast.Node) summary {
		var s summary
		ast.Inspect(n, func(m ast.Node) bool {
			switch m := m.(type) {
			case *ast.FuncLit:
				return false // its body runs when called
			case *ast.CallExpr:
				if e.callWrites(m) {
					s.calls = true
				}
			case *ast.UnaryExpr:
				if m.Op == token.ARROW {
					s.calls = true
				}
			case *ast.StarExpr:
				if tv := e.s.Info.Types[m]; !tv.IsType() {
					s.reads = true
				}
			case *ast.IndexExpr:
				switch e.s.Info.TypeOf(m.X).Underlying().(type) {
				case *types.Slice, *types.Map, *types.Pointer:
					s.reads = true
				}
			case *ast.SliceExpr:
				s.reads = true
			case *ast.SelectorExpr:
				if sel := e.s.Info.Selections[m]; sel != nil && sel.Indirect() {
					s.reads = true
				}
			case *ast.Ident:
				if v, ok := e.s.Info.Uses[m].(*types.Var); ok && e.isGlobal(v) {
					s.reads = true
				}
			}
			return true
		})
		return s
	}
	found := false
	check := func(ops []ast.Expr) {
		var sums []summary
		for _, o := range ops {
			if o != nil {
				sums = append(sums, sum(o))
			}
		}
		for i := range sums {
			for j := range sums {
				if i != j && sums[i].calls && sums[j].reads {
					found = true
				}
			}
		}
	}
	check(xs)
	for _, x := range xs {
		if x == nil {
			continue
		}
		ast.Inspect(x, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.FuncLit:
				return false
			case *ast.BinaryExpr:
				if n.Op != token.LAND && n.Op != token.LOR {
					check([]ast.Expr{n.X, n.Y})
				}
			case *ast.CallExpr:
				check(append([]ast.Expr{n.Fun}, n.Args...))
			case *ast.IndexExpr:
				check([]ast.Expr{n.X, n.Index})
			case *ast.SliceExpr:
				check([]ast.Expr{n.X, n.Low, n.High, n.Max})
			case *ast.CompositeLit:
				check(n.Elts)
			case *ast.KeyValueExpr:
				check([]ast.Expr{n.Key, n.Value})
			}
			return true
		})
	}
	return found
}

// lhsOperands: what the first phase of an assignment evaluates on its left:
// the operands of index expressions and pointer indirections. The location
// itself is written afterwards, not read.
func lhsOperands(l ast.Expr) []ast.Expr {
	switch l := ast.Unparen(l).(type) {
	case *ast.StarExpr:
		return []ast.Expr{l.X}
	case *ast.IndexExpr:
		return []ast.Expr{l.X, l.Index}
	case *ast.SelectorExpr:
		return []ast.Expr{l.X}
	}
	return nil
}

// value / condOf evaluate an expression, as a whole when its evaluation
// order is unspecified.
func (e *evaluator) value(x ast.Expr) Node {
	if e.hazard(x) {
		return e.opaqueExprs([]ast.Expr{x})[0]
	}
	return e.expr(x)
}

func (e *evaluator) condOf(x ast.Expr) Node { return e.in.atom(e.value(x)) }

func resultType(xs []ast.Expr, i int, e *evaluator) types.Type {
	if len(xs) == 1 {
		if tup, ok := e.typeOf(xs[0]).(*types.Tuple); ok {
			return tup.At(i).Type()
		}
	}
	return e.typeOf(xs[i])
}

// callWrites: whether a call may write memory the program can read. Builtins
// that only compute, conversions and a list of pure standard functions do not;
// neither does a function literal called in place whose body only computes.
func (e *evaluator) callWrites(x *ast.CallExpr) bool {
	fun := ast.Unparen(x.Fun)
	if tv := e.s.Info.Types[fun]; tv.IsType() {
		return false
	}
	if lit, ok := fun.(*ast.FuncLit); ok {
		return e.bodyWrites(lit.Body)
	}
	if id := identOf(fun); id != nil {
		switch o := e.s.Info.Uses[id].(type) {
		case *types.Builtin:
			switch o.Name() {
			case "len", "cap", "min", "max", "complex", "real", "imag", "new", "make":
				return false
			}
		case *types.Func:
			if o.Pkg() != nil && pureFuncs[o.Pkg().Path()+"."+o.Name()] {
				return false
			}
			if o.Pkg() != nil && purePkgs[o.Pkg().Path()] {
				if sig, ok := o.Type().(*types.Signature); ok && sig.Recv() == nil {
					return false
				}
			}
		}
	}
	return true
}

// bodyWrites: whether a function body may write memory outside its own
// locals - stores through pointers, to globals, to elements, sends, or calls
// that may write. Creating closures and assigning locals do not.
func (e *evaluator) bodyWrites(body *ast.BlockStmt) bool {
	writes := false
	ast.Inspect(body, func(n ast.Node) bool {
		if writes {
			return false
		}
		switch n := n.(type) {
		case *ast.FuncLit:
			return false // defining a closure runs nothing
		case *ast.CallExpr:
			if e.callWrites(n) {
				writes = true
			}
		case *ast.AssignStmt:
			for _, l := range n.Lhs {
				if id, ok := ast.Unparen(l).(*ast.Ident); ok {
					if v, ok := e.s.Info.Uses[id].(*types.Var); ok && e.isGlobal(v) {
						writes = true
					}
					continue
				}
				writes = true
			}
		case *ast.IncDecStmt:
			if _, ok := ast.Unparen(n.X).(*ast.Ident); !ok {
				writes = true
			}
		case *ast.SendStmt, *ast.GoStmt, *ast.DeferStmt:
			writes = true
		case *ast.UnaryExpr:
			if n.Op == token.ARROW {
				writes = true
			}
		}
		return true
	})
	return writes
}

// Standard functions that neither write memory the caller can see nor call
// back into the program.
var purePkgs = map[string]bool{"strings": true, "strconv": true, "unicode": true, "unicode/utf8": true, "math": true, "math/bits": true, "path": true}

var pureFuncs = map[string]bool{
	"path/filepath.Base": true, "path/filepath.Dir": true, "path/filepath.Ext": true, "path/filepath.Join": true,
	"path/filepath.Clean": true, "path/filepath.Rel": true, "path/filepath.Split": true, "path/filepath.ToSlash": true,
	"path/filepath.FromSlash": true, "path/filepath.IsAbs": true, "errors.New": true, "bytes.Equal": true,
	"bytes.Compare": true, "bytes.Contains": true, "bytes.HasPrefix": true, "bytes.HasSuffix": true, "bytes.Index": true,
}

// ---------------------------------------------------------------------------

// sameMeaning evaluates function k on both sides with one interner and
// compares the resulting nodes.
func sameMeaning(in *interner, b, a *Snapshot, c classification, k string) (bool, []string) {
	eb := newEvaluator(in, b, pick(c.before, c.removed))
	ea := newEvaluator(in, a, pick(c.after, c.added))
	same := eb.function(c.before[k]) == ea.function(c.after[k])
	var axioms []string
	for _, m := range []map[string]bool{eb.axioms, ea.axioms} {
		for ax := range m {
			axioms = append(axioms, ax)
		}
	}
	sort.Strings(axioms)
	return same, slices.Compact(axioms)
}

func pick(all map[string]*ast.FuncDecl, keys []string) map[string]*ast.FuncDecl {
	out := map[string]*ast.FuncDecl{}
	for _, k := range keys {
		out[k] = all[k]
	}
	return out
}
