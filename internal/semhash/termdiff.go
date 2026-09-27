package semhash

import (
	"fmt"
	"strings"
)

// firstDifference describes where two terms first differ (for debugging
// unproved changes).
func (in *interner) firstDifference(a, b Node) string {
	var path []string
	seen := map[[2]Node]bool{}
	var parents [][2]Node
	var walk func(a, b Node) string
	walk = func(a, b Node) string {
		if a == b || seen[[2]Node{a, b}] {
			return ""
		}
		seen[[2]Node{a, b}] = true
		ta, tb := in.t(a), in.t(b)
		// muxes testing different atoms: report the muxes, not their atoms
		if ta.op == opMux && tb.op == opMux && ta.kids[0] != tb.kids[0] {
			aa, ab := in.t(ta.kids[0]), in.t(tb.kids[0])
			if aa.op == ab.op && aa.aux == ab.aux && len(aa.kids) == len(ab.kids) {
				path = append(path, "mux atom")
				if d := walk(ta.kids[0], tb.kids[0]); d != "" {
					return d + fmt.Sprintf("\nIN MUXES:\nBEFORE:\n%s\nAFTER:\n%s", in.shallow(a, 2), in.shallow(b, 2))
				}
				path = path[:len(path)-1]
			}
			return fmt.Sprintf("%s\n  muxes test different atoms\nBEFORE:\n%s\nAFTER:\n%s", strings.Join(path, " > "), in.shallow(a, 3), in.shallow(b, 3))
		}
		parents = append(parents, [2]Node{a, b})
		if ta.op != tb.op || ta.typ != tb.typ || ta.aux != tb.aux || len(ta.kids) != len(tb.kids) {
			return fmt.Sprintf("%s\n  before: %s %s %.120q (%d kids)\n  after:  %s %s %.120q (%d kids)\nBEFORE:\n%s\nAFTER:\n%s",
				strings.Join(path, " > "), opNames[ta.op], ta.typ, ta.aux, len(ta.kids), opNames[tb.op], tb.typ, tb.aux, len(tb.kids),
				in.shallow(a, 3), in.shallow(b, 3))
		}
		for i := range ta.kids {
			path = append(path, fmt.Sprintf("%s %.40s[%d]", opNames[ta.op], ta.aux, i))
			if d := walk(ta.kids[i], tb.kids[i]); d != "" {
				return d
			}
			path = path[:len(path)-1]
		}
		return ""
	}
	return walk(a, b)
}

// shallow renders a term to a depth.
func (in *interner) shallow(n Node, depth int) string {
	var b strings.Builder
	var walk func(n Node, d int)
	walk = func(n Node, d int) {
		t := in.t(n)
		fmt.Fprintf(&b, "%s#%d %s %s %.80q\n", strings.Repeat("  ", 3-d), n, opNames[t.op], t.typ, t.aux)
		if d == 0 {
			return
		}
		for _, k := range t.kids {
			walk(k, d-1)
		}
	}
	walk(n, depth)
	return b.String()
}

// witness renders one path through guard g to true (for debugging).
func (in *interner) witness(g Node) string {
	var b strings.Builder
	for in.isMux(g) {
		t := in.t(g)
		a := t.kids[0]
		hi, lo := t.kids[1], t.kids[2]
		at := in.t(a)
		desc := fmt.Sprintf("#%d %s %q", a, at.aux, at.typ)
		for _, k := range at.kids {
			kt := in.t(k)
			desc += fmt.Sprintf(" [#%d %s %s %.60q", k, opNames[kt.op], kt.typ, kt.aux)
			for _, kk := range kt.kids {
				kkt := in.t(kk)
				desc += fmt.Sprintf(" (#%d %s %.50q)", kk, opNames[kkt.op], kkt.aux)
			}
			desc += "]"
		}
		if hi != in.F {
			fmt.Fprintf(&b, "  T %s\n", desc)
			g = hi
		} else {
			fmt.Fprintf(&b, "  F %s\n", desc)
			g = lo
		}
	}
	return b.String()
}
