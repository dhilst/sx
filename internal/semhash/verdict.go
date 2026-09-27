package semhash

import (
	"fmt"
	"slices"
	"strings"
)

// Options tune what a proof may assume.
type Options struct {
	// AllowRemovedExported: an exported function may be removed, because
	// something outside this package (sx's deadcode pass) showed nothing
	// reaches it. The verdict lists it among its axioms.
	AllowRemovedExported bool
	// Budget caps the term nodes one proof may build (0: a default).
	Budget int
}

// Verdict is the outcome of Prove.
type Verdict struct {
	Proved  bool
	Reason  string   // why not, when not proved
	Checked []string // the functions whose terms were compared
	Axioms  []string // assumptions beyond the package documentation's
}

func (v Verdict) String() string {
	if v.Proved {
		s := "proved"
		if len(v.Checked) > 0 {
			s += fmt.Sprintf(" (%d functions)", len(v.Checked))
		}
		if len(v.Axioms) > 0 {
			s += " assuming " + strings.Join(v.Axioms, "; ")
		}
		return s
	}
	return "unproved: " + v.Reason
}

// Prove decides whether after behaves as before. Proved means it does, under
// the stated assumptions; not proved means nothing either way.
func Prove(before, after *Snapshot, opt Options) (v Verdict) {
	defer func() {
		if r := recover(); r != nil {
			u, ok := r.(unproved)
			if !ok {
				panic(r)
			}
			v = Verdict{Reason: string(u)}
		}
	}()
	c := classify(before, after, opt)
	if len(c.problems) > 0 {
		return Verdict{Reason: c.problems[0]}
	}
	v.Axioms = append(v.Axioms, c.axioms...)
	if opt.AllowRemovedExported {
		for _, k := range c.removed {
			if isExportedKey(k) {
				v.Axioms = append(v.Axioms, "nothing outside the package calls "+k)
			}
		}
	}
	in := newInterner(opt.Budget)
	for _, k := range c.changed {
		v.Checked = append(v.Checked, k)
		same, axioms := sameMeaning(in, before, after, c, k)
		for _, ax := range axioms {
			if !slices.Contains(v.Axioms, ax) {
				v.Axioms = append(v.Axioms, ax)
			}
		}
		if !same {
			return Verdict{Reason: fmt.Sprintf("%s: the two versions do not normalise to the same term", k), Checked: v.Checked, Axioms: v.Axioms}
		}
	}
	v.Proved = true
	return v
}

func isExportedKey(k string) bool {
	name := k[strings.LastIndex(k, ".")+1:]
	return name != "" && name[0] >= 'A' && name[0] <= 'Z'
}

// unproved aborts a proof from deep inside the evaluator.
type unproved string

func refuse(format string, a ...any) { panic(unproved(fmt.Sprintf(format, a...))) }
