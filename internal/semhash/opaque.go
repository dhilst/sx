package semhash

import (
	"go/ast"
	"go/token"
	"go/types"
	"strconv"
)

// Opaque evaluation. A statement or expression whose meaning depends on an
// evaluation order Go leaves to the compiler (see hazard) is not taken
// apart: it becomes one effect named by its canonical syntax - resolved
// objects, locals numbered by first use - whose inputs are the values of the
// local variables it reads (the addresses of cells it touches) and the
// state, and whose outputs are the locals it assigns. The same code run on
// the same inputs from the same state behaves the same, so equal opaque
// nodes are a proof; code that differs in any way, or whose inputs are
// different terms, is not equal. What order gc picks is never assumed.

// opaqueInputs lists the local variables n reads or writes, in order of first
// use, with the values it reads them as.
func (e *evaluator) opaqueInputs(n ast.Node) (vars []types.Object, inputs []Node, writes []types.Object) {
	seen := map[types.Object]bool{}
	written := map[types.Object]bool{}
	ast.Inspect(n, func(m ast.Node) bool {
		switch m := m.(type) {
		case *ast.AssignStmt:
			for _, l := range m.Lhs {
				if o := e.rootVar(l); o != nil {
					written[o] = true
				}
			}
		case *ast.IncDecStmt:
			if o := e.rootVar(m.X); o != nil {
				written[o] = true
			}
		case *ast.Ident:
			o := e.s.Info.Uses[m]
			if o == nil {
				o = e.s.Info.Defs[m]
			}
			v, ok := o.(*types.Var)
			if !ok || !e.isLocalVar(v) || seen[v] {
				return true
			}
			seen[v] = true
			vars = append(vars, v)
		case *ast.CallExpr:
			if f, ok := e.s.Info.Uses[identOf(ast.Unparen(m.Fun))].(*types.Func); ok && e.inline[objKey(f)] != nil {
				refuse("%s, which the change added or removed, is called where the evaluation order is unspecified (%s)", objKey(f), e.pos(m))
			}
		case *ast.BranchStmt, *ast.ReturnStmt, *ast.DeferStmt, *ast.LabeledStmt:
			refuse("control flow inside code evaluated as a whole (%s)", e.pos(m))
		}
		return true
	})
	for _, v := range vars {
		val, declaredHere := e.env[v]
		switch {
		case !declaredHere: // declared by n itself
			inputs = append(inputs, e.in.mk(opConst, "none", ""))
		case e.cells[v]:
			e.escape(v) // n reads and writes it in memory
			inputs = append(inputs, val)
		default:
			inputs = append(inputs, val)
			if written[v] {
				writes = append(writes, v)
			}
		}
	}
	for _, v := range vars { // variables n declares are outputs too
		if _, ok := e.env[v]; !ok && written[v] || !ok && e.definedIn(v, n) {
			writes = append(writes, v)
		}
	}
	return vars, inputs, writes
}

func (e *evaluator) definedIn(v types.Object, n ast.Node) bool {
	return v.Pos() >= n.Pos() && v.Pos() < n.End()
}

// opaqueStmt evaluates a statement as a whole.
func (e *evaluator) opaqueStmt(s ast.Stmt) {
	_, inputs, writes := e.opaqueInputs(s)
	e.escapeReachable(inputs...)
	var ts []types.Type
	for _, w := range writes {
		ts = append(ts, w.Type())
	}
	outs := e.effect("opaque:"+e.s.canon(s), ts, inputs...)
	for i, w := range writes {
		if e.definedIn(w, s) {
			e.declare(w, outs[i])
		} else {
			e.writeVar(w, outs[i])
		}
	}
}

// opaqueExprs evaluates expressions as a whole, to their values.
func (e *evaluator) opaqueExprs(xs []ast.Expr) []Node {
	var ts []types.Type
	for _, x := range xs {
		t := e.typeOf(x)
		if tup, ok := t.(*types.Tuple); ok {
			for i := 0; i < tup.Len(); i++ {
				ts = append(ts, tup.At(i).Type())
			}
		} else {
			ts = append(ts, t)
		}
	}
	list := &ast.CompositeLit{Elts: xs} // a node holding them, for canon and inputs
	_, inputs, writes := e.opaqueInputs(list)
	if len(writes) > 0 {
		refuse("an expression assigns variables where the evaluation order is unspecified")
	}
	e.escapeReachable(inputs...)
	var key string
	for _, x := range xs {
		key += e.s.canon(x) + ";"
	}
	return e.effect("opaque:"+strconv.Itoa(len(xs))+":"+key, ts, inputs...)
}

// ---------------------------------------------------------------------------
// goto

// A backward goto to a label in the same statement list (goto L jumping up
// to "L: stmt") makes the statements from the label to the end of the list a
// loop that runs again at each such goto and ends when control falls off
// the list: evaluated as a synthetic loop whose continue is the goto. A
// forward goto leaves the statements between it and its label, as an exit
// that resumes at the label.

// gotoTargets: labels in list whose statement a goto inside list[i:] jumps
// back to.
func (e *evaluator) backwardLabel(list []ast.Stmt, i int) (*ast.LabeledStmt, bool) {
	l, ok := list[i].(*ast.LabeledStmt)
	if !ok {
		return nil, false
	}
	back := false
	for _, s := range list[i:] {
		ast.Inspect(s, func(n ast.Node) bool {
			if _, ok := n.(*ast.FuncLit); ok {
				return false
			}
			if b, ok := n.(*ast.BranchStmt); ok && b.Tok == token.GOTO && b.Label.Name == l.Label.Name && b.Pos() > l.Pos() {
				back = true
			}
			return true
		})
	}
	return l, back
}

// gotoLoop runs list[i:] as the loop a backward goto makes of it. Falling
// off the end of the list leaves the loop: a break at the end of the body,
// so it is not taken by the gotos, which continue the loop.
func (e *evaluator) gotoLoop(l *ast.LabeledStmt, rest []ast.Stmt) {
	end := rest[len(rest)-1].End()
	stmts := append(append([]ast.Stmt(nil), rest...), &ast.BranchStmt{TokPos: end, Tok: token.BREAK})
	body := &ast.BlockStmt{Lbrace: rest[0].Pos(), List: stmts, Rbrace: end}
	synth := &ast.ForStmt{For: rest[0].Pos(), Body: body}
	e.gotoLoops[l.Label.Name] = synth
	defer delete(e.gotoLoops, l.Label.Name)
	e.loop(synth, nil, func() Node { return e.in.T }, func() {}, body, func() {})
}

// forwardGoto records a goto to a label later in an enclosing list.
func (e *evaluator) forwardGoto(s *ast.BranchStmt) {
	e.leave(exit{kind: token.GOTO, label: s.Label.Name})
}

// resumeAt merges the forward gotos to label into the flow reaching it.
func (e *evaluator) resumeAt(label string) {
	var mine, rest []exit
	for _, x := range e.exits {
		if x.kind == token.GOTO && x.label == label {
			mine = append(mine, x)
		} else {
			rest = append(rest, x)
		}
	}
	if len(mine) == 0 {
		return
	}
	before := e.cloneEnv()
	for _, x := range mine {
		for k, v := range x.env {
			if _, ok := before[k]; !ok {
				before[k] = v
			}
		}
	}
	e.exits = rest
	parts := []part{{e.g, e.st, e.env}}
	for _, x := range mine {
		parts = append(parts, part{x.guard, x.st, x.env})
	}
	e.merge(parts, before)
}
