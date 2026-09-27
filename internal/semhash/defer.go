package semhash

import (
	"go/ast"
	"go/token"
	"go/types"
	"strconv"
)

// Defers belong to a frame. A defer statement evaluates the function and its
// arguments where it stands and registers the call - an effect, so a path
// that panicked earlier does not register it - yielding a token. The frame
// keeps (guard, token) in the order registered; at each return of the frame
// the calls run in reverse, each under the guard it was registered under.
// Named results are read after the deferred calls, which may change them.
// An inlined function has its own frame, so a helper's defers run when the
// helper returns, exactly as they did before it was inlined or extracted.

type deferRec struct {
	guard Node
	token Node
}

func (e *evaluator) deferStmt(s *ast.DeferStmt) {
	if e.depth > e.frameDepth {
		refuse("defer inside a loop is not modelled (%s)", e.pos(s))
	}
	if e.hazard(append([]ast.Expr{s.Call.Fun}, s.Call.Args...)...) {
		refuse("a deferred call whose evaluation order is unspecified (%s)", e.pos(s))
	}
	fn, args := e.deferredCall(s.Call)
	tok := e.effect("defer", []types.Type{types.Typ[types.UnsafePointer]}, append([]Node{fn}, args...)...)[0]
	e.defers = append(e.defers, deferRec{e.g, tok})
}

func (e *evaluator) goStmt(s *ast.GoStmt) {
	if e.hazard(append([]ast.Expr{s.Call.Fun}, s.Call.Args...)...) {
		refuse("a go statement whose evaluation order is unspecified (%s)", e.pos(s))
	}
	fn, args := e.deferredCall(s.Call)
	e.effect("go", nil, append([]Node{fn}, args...)...)
}

// deferredCall evaluates the function value and the arguments of a deferred
// or spawned call.
func (e *evaluator) deferredCall(x *ast.CallExpr) (Node, []Node) {
	fun := ast.Unparen(x.Fun)
	if id := identOf(fun); id != nil {
		if b, ok := e.s.Info.Uses[id].(*types.Builtin); ok {
			if b.Name() == "recover" {
				refuse("a deferred recover() at %s", e.pos(x))
			}
			var args []Node
			for _, a := range x.Args {
				args = append(args, e.expr(a))
			}
			return e.in.mk(opConst, "builtin", b.Name()), args
		}
	}
	sig, ok := e.typeOf(fun).Underlying().(*types.Signature)
	if !ok {
		refuse("a deferred call of a non-function at %s", e.pos(x))
	}
	var fn Node
	switch f := fun.(type) {
	case *ast.FuncLit:
		fn = e.funcLit(f)
	default:
		fn = e.expr(f) // a function, a bound method, a function value
	}
	return fn, e.args(x, sig)
}

// runDefers runs the current frame's deferred calls, newest first.
func (e *evaluator) runDefers() {
	for i := len(e.defers) - 1; i >= 0; i-- {
		d := e.defers[i]
		// Inside a loop body the guard is relative to the loop's entry;
		// reach is the path that entered it, so a defer registered on a
		// path that never enters the loop is not one the body can run.
		// (dg only agrees with the guard on reach: decide with the guard)
		dg := e.in.under(d.guard, e.reach)
		// Running a deferred call on a state chosen by an earlier condition is
		// choosing between the call run on each state: lift it through the
		// state's muxes, so a defer run once after paths merge (a helper that
		// returned) and one run on each path before they merge are one term.
		run := func(st Node) Node {
			return e.in.lift([]Node{st}, func(l []Node) Node {
				return e.in.mk(opProj, "state", "0", e.in.mk(opEffect, "effect", "rundefer", l[0], d.token))
			})
		}
		switch {
		case e.in.and(d.guard, e.in.and(e.g, e.reach)) == e.in.F:
			continue // registered on another path
		case e.in.and(e.in.and(e.g, e.reach), e.in.not(d.guard)) == e.in.F: // registered on every path here
			e.st = run(e.st)
		default:
			e.st = e.in.under(e.in.ite(dg, run(e.st), e.st), e.g)
		}
	}
}

func (e *evaluator) selectStmt(s *ast.SelectStmt) {
	// every channel and value to send, once, in source order
	var ops, recvs []Node
	var results []types.Type
	desc := "select"
	type recv struct {
		clause int
		assign *ast.AssignStmt
		elem   types.Type
	}
	var rs []recv
	def := -1
	for i, c := range s.Body.List {
		cc := c.(*ast.CommClause)
		switch comm := cc.Comm.(type) {
		case nil:
			def = i
			desc += ":default"
		case *ast.SendStmt:
			elem := e.typeOf(comm.Chan).Underlying().(*types.Chan).Elem()
			ops = append(ops, e.expr(comm.Chan), e.conv(e.expr(comm.Value), e.typeOf(comm.Value), elem))
			desc += ":send"
		case *ast.ExprStmt:
			ch := ast.Unparen(comm.X).(*ast.UnaryExpr).X
			ops = append(ops, e.expr(ch))
			rs = append(rs, recv{i, nil, e.typeOf(ch).Underlying().(*types.Chan).Elem()})
			desc += ":recv"
		case *ast.AssignStmt:
			ch := ast.Unparen(comm.Rhs[0]).(*ast.UnaryExpr).X
			ops = append(ops, e.expr(ch))
			rs = append(rs, recv{i, comm, e.typeOf(ch).Underlying().(*types.Chan).Elem()})
			desc += ":recv"
		}
	}
	results = append(results, types.Typ[types.Int])
	for _, r := range rs {
		results = append(results, r.elem, types.Typ[types.Bool])
	}
	out := e.effect(desc, results, ops...)
	idx := out[0]
	recvs = out[1:]
	intT := types.Typ[types.Int]
	var try func(i int)
	try = func(i int) {
		if i == len(s.Body.List) {
			return
		}
		cc := s.Body.List[i].(*ast.CommClause)
		run := func() {
			for j, r := range rs {
				if r.clause == i && r.assign != nil {
					vals := []Node{recvs[2*j], recvs[2*j+1]}
					for k, l := range r.assign.Lhs {
						if id, ok := l.(*ast.Ident); ok && id.Name == "_" {
							continue
						}
						if r.assign.Tok == token.DEFINE {
							e.declare(e.s.Info.Defs[l.(*ast.Ident)], vals[k])
						} else {
							_, set := e.lvalue(l)
							set(vals[k])
						}
					}
				}
			}
			e.block(cc.Body)
		}
		if i == def {
			// the default clause is taken when idx says so, like the others
		}
		c := e.in.atom(e.in.mk(opPure, "bool", "cmp==", idx, e.in.mk(opConst, e.key(intT), strconv.Itoa(i))))
		e.branch(c, run, func() { try(i + 1) })
	}
	e.breakable(s, func() { try(0) })
}
