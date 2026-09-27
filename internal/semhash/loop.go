package semhash

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"sort"
	"strconv"
)

// Loops are terms binding their carried variables. A loop node holds the
// carried variables' initial values, the initial state, and the body: a term
// over bound variables (the carried values and state at the start of an
// iteration, numbered by loop depth) that says, as an MTBDD over the
// iteration's guards, how the iteration ends - on to the next iteration,
// out of the loop, out of the function, or out to an enclosing loop - with
// the carried values, the state and any results at that point. The loop
// node means the iteration of that body from those values, so two loops with
// the same node behave the same.
//
// The carried variables are those assigned in the loop that were declared
// before its body, in the order the loop first assigns them; one whose value
// at the start of an iteration is never read, and that nothing outside the
// loop reads, is not carried. Hidden counters of range loops come first.

type hiddenVar struct {
	obj  *types.Var
	init Node
}

func (e *evaluator) newHidden(name string, t types.Type, init Node) hiddenVar {
	return hiddenVar{types.NewVar(token.NoPos, nil, name, t), init}
}

func (e *evaluator) forStmt(s *ast.ForStmt) {
	if s.Init != nil {
		e.stmt(s.Init)
		if a, ok := s.Init.(*ast.AssignStmt); ok && a.Tok == token.DEFINE {
			for _, l := range a.Lhs {
				if o := e.s.Info.Defs[l.(*ast.Ident)]; o != nil && e.cells[o] {
					refuse("the loop variable %s has its address taken; per-iteration copies are not modelled (%s)", o.Name(), e.pos(s))
				}
			}
		}
	}
	e.loop(s, nil, func() Node {
		if s.Cond == nil {
			return e.in.T
		}
		return e.condOf(s.Cond)
	}, func() {}, s.Body, func() {
		if s.Post != nil {
			e.stmt(s.Post)
		}
	})
}

func (e *evaluator) rangeStmt(s *ast.RangeStmt) {
	xt := e.typeOf(s.X)
	intT := types.Typ[types.Int]
	x0 := e.value(s.X) // evaluated once
	var k, v Node = -1, -1
	assign := func() {
		for i, x := range []ast.Expr{s.Key, s.Value} {
			val := k
			if i == 1 {
				val = v
			}
			if x == nil || val < 0 {
				continue
			}
			if id, ok := x.(*ast.Ident); ok && id.Name == "_" {
				continue
			}
			if s.Tok == token.DEFINE {
				e.declare(e.s.Info.Defs[x.(*ast.Ident)], val)
			} else {
				_, set := e.lvalue(x)
				set(val)
			}
		}
	}
	one := e.in.mk(opConst, e.key(intT), "1")
	switch u := xt.Underlying().(type) {
	case *types.Basic:
		switch {
		case u.Info()&types.IsInteger != 0:
			i := e.newHidden("i", xt, e.in.mk(opConst, e.key(xt), "0"))
			e.loop(s, []hiddenVar{i}, func() Node {
				return e.compare(token.LSS, e.env[i.obj], x0, xt, xt, s)
			}, func() { k = e.env[i.obj]; assign() }, s.Body, func() {
				e.env[i.obj] = e.pure("binary:+", xt, e.env[i.obj], e.in.mk(opConst, e.key(xt), "1"))
			})
			return
		case u.Info()&types.IsString != 0:
			off := e.newHidden("off", intT, e.in.mk(opConst, e.key(intT), "0"))
			n := e.pure("len", intT, x0)
			runeT := types.Typ[types.Rune]
			e.loop(s, []hiddenVar{off}, func() Node {
				return e.compare(token.LSS, e.env[off.obj], n, intT, intT, s)
			}, func() {
				k, v = e.env[off.obj], e.pure("runeat", runeT, x0, e.env[off.obj])
				assign()
			}, s.Body, func() {
				e.env[off.obj] = e.pure("binary:+", intT, e.env[off.obj], e.pure("runewidth", intT, x0, e.env[off.obj]))
			})
			return
		}
	case *types.Slice, *types.Array, *types.Pointer:
		var n Node
		var elemT types.Type
		switch u := u.(type) {
		case *types.Slice:
			n, elemT = e.pure("len", intT, x0), u.Elem()
		case *types.Array:
			n, elemT = e.in.mk(opConst, e.key(intT), strconv.FormatInt(u.Len(), 10)), u.Elem()
		case *types.Pointer:
			arr, ok := u.Elem().Underlying().(*types.Array)
			if !ok {
				break
			}
			n, elemT = e.in.mk(opConst, e.key(intT), strconv.FormatInt(arr.Len(), 10)), arr.Elem()
		}
		if elemT == nil {
			break
		}
		i := e.newHidden("i", intT, e.in.mk(opConst, e.key(intT), "0"))
		_, isArray := u.(*types.Array)
		e.loop(s, []hiddenVar{i}, func() Node {
			return e.compare(token.LSS, e.env[i.obj], n, intT, intT, s)
		}, func() {
			k = e.env[i.obj]
			if s.Value != nil {
				if isArray { // a copy of the array, indexed within bounds
					v = e.pure("arrayat", elemT, x0, k)
				} else {
					v = e.effect("index", []types.Type{elemT}, x0, k)[0]
				}
			}
			assign()
		}, s.Body, func() {
			e.env[i.obj] = e.pure("binary:+", intT, e.env[i.obj], one)
		})
		return
	case *types.Map:
		it := e.effect("mapiter", []types.Type{types.Typ[types.UnsafePointer]}, x0)[0]
		e.loop(s, nil, func() Node {
			r := e.effect("mapnext", []types.Type{u.Key(), u.Elem(), types.Typ[types.Bool]}, it)
			k, v = r[0], r[1]
			return r[2]
		}, assign, s.Body, func() {})
		return
	case *types.Chan:
		e.loop(s, nil, func() Node {
			r := e.effect("recv2", []types.Type{u.Elem(), types.Typ[types.Bool]}, x0)
			k = r[0]
			return r[1]
		}, assign, s.Body, func() {})
		return
	}
	refuse("range over %s is not modelled (%s)", xt, e.pos(s))
}

// loop evaluates a loop: test at the start of each iteration, bind the
// iteration's variables, the body, then post.
func (e *evaluator) loop(s ast.Stmt, hidden []hiddenVar, test func() Node, bind func(), body *ast.BlockStmt, post func()) {
	e.escapeAll(s)
	for _, h := range hidden {
		e.env[h.obj] = h.init
	}
	carried := append([]types.Object(nil), hiddenObjs(hidden)...)
	carried = append(carried, e.carriedVars(s, body)...)

	depth := e.depth
	e.depth++
	outerEnv, g0, st0, exits0, outer0 := e.cloneEnv(), e.g, e.st, e.exits, e.outer
	inits := make([]Node, len(carried))
	for i, c := range carried {
		inits[i] = e.env[c]
		e.env[c] = e.norm(e.in.mk(opBound, e.key(c.Type()), fmt.Sprintf("%d.%d", depth, i)), c.Type())
	}
	stIn := e.in.mk(opBound, "state", fmt.Sprintf("%d.st", depth))
	e.st, e.g, e.exits, e.outer = stIn, e.in.T, nil, append(append([]ast.Stmt(nil), outer0...), s)

	reach0 := e.reach
	e.reach = e.in.and(reach0, g0)
	c := test()
	e.branch(c, func() {
		bind()
		e.block(body.List)
		e.joinExits(s, token.CONTINUE)
		if e.g != e.in.F {
			post()
			if e.g != e.in.F {
				e.leave(exit{kind: token.CONTINUE, tag: "next"})
			}
		}
	}, func() {
		e.leave(exit{kind: token.BREAK, tag: "done"})
	})
	exits := e.exits
	e.depth--
	e.reach = reach0

	// what the body's exits carry
	tagOf := func(x exit) string {
		switch {
		case x.tag != "":
			return x.tag
		case x.kind == token.RETURN:
			return "return"
		case x.target == s && x.kind == token.BREAK:
			return "done"
		}
		for i := len(outer0) - 1; i >= 0; i-- {
			if outer0[i] == x.target {
				return fmt.Sprintf("%s:%d", x.kind, len(outer0)-1-i)
			}
		}
		refuse("a jump out of a loop to an unknown statement")
		return ""
	}
	build := func(keep []int) Node {
		var parts []exit
		for _, x := range exits {
			vals := []Node{e.in.mk(opConst, "tag", tagOf(x))}
			for _, i := range keep {
				vals = append(vals, x.env[carried[i]])
			}
			if x.kind == token.RETURN { // only a return carries results
				vals = append(vals, x.vals...)
			}
			parts = append(parts, exit{guard: x.guard, st: x.st, vals: vals})
		}
		return e.fold(parts)
	}
	all := make([]int, len(carried))
	for i := range all {
		all[i] = i
	}
	bodyT := build(all)
	// drop carried variables whose incoming value nothing reads
	var keep []int
	for i, c := range carried {
		b := e.in.mk(opBound, e.key(c.Type()), fmt.Sprintf("%d.%d", depth, i))
		if i < len(hidden) || e.mentions(bodyT, b) || e.mentions(bodyT, e.norm(b, c.Type())) || e.usedOutside(c, s) {
			keep = append(keep, i)
		}
	}
	if len(keep) < len(carried) {
		// renumber the kept variables' bound nodes: rebuild from scratch
		kept := make([]types.Object, len(keep))
		for j, i := range keep {
			kept[j] = carried[i]
		}
		e.env, e.g, e.st, e.exits, e.outer = outerEnv, g0, st0, exits0, outer0
		e.loopWith(s, hidden, kept, test, bind, body, post)
		return
	}
	var ops []Node
	for _, i := range keep {
		ops = append(ops, inits[i])
	}
	ops = append(ops, st0, bodyT)
	loopN := e.in.mk(opLoop, "tuple", fmt.Sprintf("loop:%d", depth), ops...)
	e.afterLoop(s, loopN, carried, keep, outerEnv, g0, exits0, outer0, exits, tagOf)
}

func hiddenObjs(h []hiddenVar) []types.Object {
	out := make([]types.Object, len(h))
	for i, x := range h {
		out[i] = x.obj
	}
	return out
}

// loopWith re-evaluates a loop with a given carried set (after pruning).
func (e *evaluator) loopWith(s ast.Stmt, hidden []hiddenVar, carried []types.Object, test func() Node, bind func(), body *ast.BlockStmt, post func()) {
	saved := e.forceCarried
	e.forceCarried = carried
	e.loop(s, hidden, test, bind, body, post)
	e.forceCarried = saved
}

// afterLoop continues after the loop node: the code after the loop runs
// where the loop finished, and the loop's other exits become exits here.
func (e *evaluator) afterLoop(s ast.Stmt, loopN Node, carried []types.Object, keep []int, outerEnv map[types.Object]Node, g0 Node, exits0 []exit, outer0 []ast.Stmt, exits []exit, tagOf func(exit) string) {
	e.env, e.g, e.exits, e.outer = outerEnv, g0, exits0, outer0
	tags := map[string]exit{}
	for _, x := range exits {
		tags[tagOf(x)] = x
	}
	var names []string
	for t := range tags {
		if t != "next" { // the next iteration: it never leaves the loop
			names = append(names, t)
		}
	}
	sort.Strings(names)
	tagN := e.in.mk(opProj, "tag", "0", loopN)
	env := e.cloneEnv()
	for j, i := range keep {
		c := carried[i]
		env[c] = e.norm(e.in.mk(opProj, e.key(c.Type()), strconv.Itoa(1+j), loopN), c.Type())
	}
	stN := e.in.mk(opProj, "state", strconv.Itoa(1+len(keep)), loopN)
	results := e.sig.Results()
	e.st = stN
	fall := e.in.F
	// The tag has exactly one value, so the exits partition the space: the
	// i-th tag is "not any earlier one and this one", the last is "none of
	// the earlier ones". Independent atoms would leave impossible
	// combinations (two tags at once) that two equal loops could fill
	// differently.
	rest := e.in.T
	for k, t := range names {
		g := e.in.and(g0, rest)
		if k < len(names)-1 {
			is := e.in.atom(e.in.mk(opPure, "bool", "cmp==", tagN, e.in.mk(opConst, "tag", t)))
			g = e.in.and(g, is)
			rest = e.in.and(rest, e.in.not(is))
		}
		x := tags[t]
		switch {
		case t == "done":
			fall = g
		case t == "return":
			var vals []Node
			for j := 0; j < results.Len(); j++ {
				rt := results.At(j).Type()
				vals = append(vals, e.norm(e.in.mk(opProj, e.key(rt), strconv.Itoa(2+len(keep)+j), loopN), rt))
			}
			e.exits = append(e.exits, exit{kind: token.RETURN, guard: g, st: stN, env: env, vals: vals})
		default: // a break or continue to an enclosing statement
			e.exits = append(e.exits, exit{kind: x.kind, target: x.target, guard: g, st: stN, env: env})
		}
	}
	e.env = env
	e.g = fall
	if fall != e.in.F {
		e.restrictToGuard()
	}
}

// carriedVars: variables declared before the loop body and assigned in the
// loop, in the order the loop first assigns them.
func (e *evaluator) carriedVars(s ast.Stmt, body *ast.BlockStmt) []types.Object {
	if e.forceCarried != nil {
		return e.forceCarried
	}
	var out []types.Object
	seen := map[types.Object]bool{}
	defined := map[types.Object]bool{}
	if r, ok := s.(*ast.RangeStmt); ok && r.Tok == token.DEFINE {
		for _, x := range []ast.Expr{r.Key, r.Value} {
			if id, ok := x.(*ast.Ident); ok {
				defined[e.s.Info.Defs[id]] = true
			}
		}
	}
	add := func(x ast.Expr) {
		o := e.rootVar(x)
		if o == nil || seen[o] || defined[o] || e.cells[o] || o.Pos() >= body.Pos() {
			return
		}
		if _, ok := e.env[o]; !ok {
			return
		}
		seen[o] = true
		out = append(out, o)
	}
	ast.Inspect(s, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			return false // what a closure assigns is a cell
		case *ast.AssignStmt:
			for _, l := range n.Lhs {
				if id, ok := l.(*ast.Ident); ok && n.Tok == token.DEFINE && e.s.Info.Defs[id] != nil {
					continue
				}
				add(l)
			}
		case *ast.IncDecStmt:
			add(n.X)
		case *ast.RangeStmt:
			if n.Tok == token.ASSIGN {
				for _, x := range []ast.Expr{n.Key, n.Value} {
					if x != nil {
						add(x)
					}
				}
			}
		}
		return true
	})
	return out
}

// usedOutside: something outside the loop reads o (after it, or before it in
// an enclosing loop's next iteration).
func (e *evaluator) usedOutside(o types.Object, s ast.Stmt) bool {
	used := false
	ast.Inspect(e.root, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && e.s.Info.Uses[id] == o && (id.Pos() < s.Pos() || id.Pos() >= s.End()) {
			used = true
		}
		return !used
	})
	return used
}

// mentions: whether term t contains node n.
func (e *evaluator) mentions(t, n Node) bool {
	seen := map[Node]bool{}
	var walk func(Node) bool
	walk = func(x Node) bool {
		if x == n {
			return true
		}
		if seen[x] {
			return false
		}
		seen[x] = true
		for _, k := range e.in.t(x).kids {
			if walk(k) {
				return true
			}
		}
		return false
	}
	return walk(t)
}

// joinExits merges the exits of kind k targeting s back into the flow.
func (e *evaluator) joinExits(s ast.Stmt, k token.Token) {
	var mine, rest []exit
	for _, x := range e.exits {
		if x.kind == k && x.target == s && x.tag == "" {
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
