package semhash

import (
	"cmp"
	"fmt"
	"strings"
)

// Node is an interned term. Equal nodes are the same term, and a term's
// meaning is a function of the term alone, so equal nodes mean the same.
type Node int32

type op uint8

const (
	opConst  op = iota // aux: the exact constant; typ: its type
	opZero             // the zero value of typ
	opParam            // aux: the parameter's position (-1: receiver)
	opState0           // the state a function starts from
	opMux              // kids: atom, hi, lo. Ordered by atom (a bool term that is not a mux)
	opPure             // aux: operator; kids: operands. No effect, cannot panic
	opEffect           // aux: operation; kids: state, operands. A tuple of results then the new state
	opProj             // aux: index; kids: a tuple
	opTuple            // kids: elements
	opFunc             // aux: a function's key, as a value
	opOpaque           // aux: canonical syntax; kids: the inputs it reads
	opLoop             // aux: depth; kids: carried initial values, initial state, body
	opBound            // aux: "depth.i" or "depth.st": a loop's carried value or state
	opLambda           // aux: binder level; kids: the body over its bound parameters and state
)

var opNames = [...]string{"const", "zero", "param", "state0", "mux", "pure", "effect", "proj", "tuple", "func", "opaque", "loop", "bound", "lambda"}

type term struct {
	op   op
	typ  string
	aux  string
	kids []Node
}

// interner hash-conses terms: one Node per distinct term, in creation order.
// The order of Nodes is also the order of guard atoms, so it is global to one
// proof: both versions are evaluated with the same interner.
type interner struct {
	terms  []term
	index  map[string]Node
	budget int
	// memo tables of the MTBDD operations
	iteMemo  map[[3]Node]Node
	restMemo map[[3]Node]Node
	ordMemo  map[[2]Node]int

	T, F Node // the boolean constants
}

func newInterner(budget int) *interner {
	if budget <= 0 {
		budget = 2_000_000
	}
	in := &interner{index: map[string]Node{}, budget: budget, iteMemo: map[[3]Node]Node{}, restMemo: map[[3]Node]Node{}, ordMemo: map[[2]Node]int{}}
	in.T = in.mk(opConst, "bool", "true")
	in.F = in.mk(opConst, "bool", "false")
	return in
}

func (in *interner) mk(o op, typ, aux string, kids ...Node) Node {
	var b strings.Builder
	fmt.Fprintf(&b, "%d|%s|%s", o, typ, aux)
	for _, k := range kids {
		fmt.Fprintf(&b, "|%d", k)
	}
	key := b.String()
	if n, ok := in.index[key]; ok {
		return n
	}
	if len(in.terms) >= in.budget {
		refuse("the terms outgrew the budget of %d nodes", in.budget)
	}
	n := Node(len(in.terms))
	in.terms = append(in.terms, term{o, typ, aux, append([]Node(nil), kids...)})
	in.index[key] = n
	return n
}

func (in *interner) t(n Node) term { return in.terms[n] }

func (in *interner) isMux(n Node) bool { return in.terms[n].op == opMux }

// String renders a term for debugging and for the printable fingerprint.
func (in *interner) String(n Node) string {
	var b strings.Builder
	seen := map[Node]bool{}
	var walk func(n Node, depth int)
	walk = func(n Node, depth int) {
		t := in.terms[n]
		fmt.Fprintf(&b, "%s#%d %s %s %q", strings.Repeat("  ", depth), n, opNames[t.op], t.typ, t.aux)
		if seen[n] && len(t.kids) > 0 {
			b.WriteString(" (above)\n")
			return
		}
		seen[n] = true
		b.WriteString("\n")
		for _, k := range t.kids {
			walk(k, depth+1)
		}
	}
	walk(n, 0)
	return b.String()
}

// order compares two terms by structure (op, type, aux, then children), not
// by node number: numbers depend on the order terms were made, which differs
// between two versions of a function, while the structure of equal subterms
// does not. Used to put the operands of a symmetric operation in one order.
func (in *interner) order(a, b Node) int {
	if a == b {
		return 0
	}
	key := [2]Node{a, b}
	if r, ok := in.ordMemo[key]; ok {
		return r
	}
	ta, tb := in.t(a), in.t(b)
	r := cmp.Compare(ta.op, tb.op)
	if r == 0 {
		r = strings.Compare(ta.typ, tb.typ)
	}
	if r == 0 {
		r = strings.Compare(ta.aux, tb.aux)
	}
	if r == 0 {
		r = cmp.Compare(len(ta.kids), len(tb.kids))
	}
	for i := 0; r == 0 && i < len(ta.kids); i++ {
		r = in.order(ta.kids[i], tb.kids[i])
	}
	in.ordMemo[key] = r
	return r
}
