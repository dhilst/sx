package semhash

// Guards and branch-dependent values are multi-terminal BDDs: a mux node
// tests an atom (a boolean term that is not itself a mux) and atoms are
// ordered by their Node number, so every function of the atoms has exactly one
// representation. That is what makes an early-return flag, an error re-test or
// a control-flow code fold back into the shape of the original branch.

// atom turns a boolean term into its BDD.
func (in *interner) atom(t Node) Node {
	switch {
	case t == in.T || t == in.F || in.isMux(t):
		return t
	}
	return in.mkMux(t, in.T, in.F)
}

func (in *interner) mkMux(a, hi, lo Node) Node {
	if hi == lo {
		return hi
	}
	return in.mk(opMux, in.t(hi).typ, "", a, hi, lo)
}

// top is the smallest atom tested at the root of n, or -1.
func (in *interner) top(n Node) Node {
	if in.isMux(n) {
		return in.terms[n].kids[0]
	}
	return -1
}

// cof splits n on atom a (which must be the smallest atom involved).
func (in *interner) cof(n, a Node) (hi, lo Node) {
	if in.isMux(n) && in.terms[n].kids[0] == a {
		return in.terms[n].kids[1], in.terms[n].kids[2]
	}
	return n, n
}

func minAtom(ns ...Node) Node {
	m := Node(-1)
	for _, n := range ns {
		if n >= 0 && (m < 0 || n < m) {
			m = n
		}
	}
	return m
}

// ite: if f then g else h, for a boolean BDD f and MTBDDs g, h of one type.
func (in *interner) ite(f, g, h Node) Node {
	switch {
	case f == in.T:
		return g
	case f == in.F:
		return h
	case g == h:
		return g
	case g == in.T && h == in.F:
		return f
	}
	key := [3]Node{f, g, h}
	if r, ok := in.iteMemo[key]; ok {
		return r
	}
	a := minAtom(in.top(f), in.top(g), in.top(h))
	if a < 0 { // f is a boolean term not yet in BDD form
		r := in.ite(in.atom(f), g, h)
		in.iteMemo[key] = r
		return r
	}
	f1, f0 := in.cof(f, a)
	g1, g0 := in.cof(g, a)
	h1, h0 := in.cof(h, a)
	r := in.mkMux(a, in.ite(f1, g1, h1), in.ite(f0, g0, h0))
	in.iteMemo[key] = r
	return r
}

func (in *interner) not(f Node) Node    { return in.ite(f, in.F, in.T) }
func (in *interner) and(f, g Node) Node { return in.ite(f, g, in.F) }
func (in *interner) or(f, g Node) Node  { return in.ite(f, in.T, g) }

// restrict sets atom a to val throughout the MTBDD n.
func (in *interner) restrict(n, a Node, val bool) Node {
	if !in.isMux(n) {
		return n
	}
	t := in.terms[n]
	switch {
	case t.kids[0] == a:
		if val {
			return t.kids[1]
		}
		return t.kids[2]
	case t.kids[0] > a:
		return n // atoms only grow downwards: a is not below
	}
	v := Node(0)
	if val {
		v = 1
	}
	key := [3]Node{n, a, v}
	if r, ok := in.restMemo[key]; ok {
		return r
	}
	r := in.mkMux(t.kids[0], in.restrict(t.kids[1], a, val), in.restrict(t.kids[2], a, val))
	in.restMemo[key] = r
	return r
}

// cube lists the literals of a guard that is a conjunction of literals
// (atom -> value), or reports false for any other guard.
func (in *interner) cube(g Node) (map[Node]bool, bool) {
	lits := map[Node]bool{}
	for g != in.T {
		if !in.isMux(g) {
			return nil, false
		}
		t := in.terms[g]
		switch {
		case t.kids[2] == in.F:
			lits[t.kids[0]] = true
			g = t.kids[1]
		case t.kids[1] == in.F:
			lits[t.kids[0]] = false
			g = t.kids[2]
		default:
			return nil, false
		}
	}
	return lits, true
}

// under simplifies n for use where guard g holds: the generalised cofactor
// (Coudert and Madre's restrict). Branches of n where g cannot hold are
// dropped, and atoms only g tests are quantified away, so a value computed
// under a condition carries no trace of paths that cannot reach it. Sound
// because code guarded by g only ever uses n where g holds; a function of
// (n, g) as BDDs, so equal inputs give equal results.
func (in *interner) under(n, g Node) Node {
	if g == in.T || g == in.F || !in.isMux(n) {
		return n
	}
	key := [3]Node{n, g, -2}
	if r, ok := in.restMemo[key]; ok {
		return r
	}
	var r Node
	a := minAtom(in.top(n), in.top(g))
	g1, g0 := in.cof(g, a)
	switch {
	case g1 == in.F: // where we care, a is false
		_, n0 := in.cof(n, a)
		r = in.under(n0, g0)
	case g0 == in.F: // where we care, a is true
		n1, _ := in.cof(n, a)
		r = in.under(n1, g1)
	case in.top(n) != a: // only g tests a: it tells nothing about n
		r = in.under(n, in.or(g1, g0))
	default:
		n1, n0 := in.cof(n, a)
		r = in.mkMux(a, in.under(n1, g1), in.under(n0, g0))
	}
	in.restMemo[key] = r
	return r
}

// lift applies a pure n-ary operation leafwise through the muxes of its
// operands, so that an operation on branch-dependent values is itself a
// canonical MTBDD. leaf builds the operation on non-mux operands.
func (in *interner) lift(ops []Node, leaf func([]Node) Node) Node {
	a := Node(-1)
	for _, o := range ops {
		a = minAtom(a, in.top(o))
	}
	if a < 0 {
		return leaf(ops)
	}
	hi, lo := make([]Node, len(ops)), make([]Node, len(ops))
	for i, o := range ops {
		hi[i], lo[i] = in.cof(o, a)
	}
	// ite, not mkMux: a boolean leaf may itself be an atom smaller than a
	return in.ite(in.atom(a), in.lift(hi, leaf), in.lift(lo, leaf))
}
