package semhash

import (
	"fmt"
	"go/ast"
	"go/token"
	"slices"
	"sort"
	"strings"
)

// classification splits the package's functions by what the change did to
// them, and lists what the change did that no evaluation can excuse.
type classification struct {
	common   []string // on both sides with the same canonical syntax
	changed  []string // on both sides, spelled differently
	added    []string // only after: a function the change introduced
	removed  []string // only before: inlined away or dead
	problems []string
	axioms   []string // what dropping or allowing something rests on
	before   map[string]*ast.FuncDecl
	after    map[string]*ast.FuncDecl
}

func classify(b, a *Snapshot, opt Options) classification {
	c := classification{before: b.funcs(), after: a.funcs()}
	for k, fb := range c.before {
		fa, ok := c.after[k]
		switch {
		case !ok:
			c.removed = append(c.removed, k)
		case b.canon(fb) == a.canon(fa):
			c.common = append(c.common, k)
		default:
			c.changed = append(c.changed, k)
		}
	}
	for k := range c.after {
		if _, ok := c.before[k]; !ok {
			c.added = append(c.added, k)
		}
	}
	for _, l := range [][]string{c.common, c.changed, c.added, c.removed} {
		sort.Strings(l)
	}
	for _, k := range c.added {
		if p := plainFunc(c.after[k]); p != "" {
			c.problems = append(c.problems, fmt.Sprintf("the change adds %s, %s", k, p))
		}
	}
	for _, k := range c.removed {
		fd := c.before[k]
		if p := plainFunc(fd); p != "" {
			c.problems = append(c.problems, fmt.Sprintf("the change removes %s, %s", k, p))
		} else if ast.IsExported(fd.Name.Name) && !opt.AllowRemovedExported {
			c.problems = append(c.problems, fmt.Sprintf("the change removes exported %s, which another package may call", k))
		}
	}
	if d := diffMaps(b.decls(), a.decls()); d != "" {
		c.problems = append(c.problems, "package declarations differ: "+d)
	}
	if !slices.Equal(b.initOrder(), a.initOrder()) {
		c.problems = append(c.problems, "package variables are initialised differently")
	}
	if b.Deps != nil && a.Deps != nil && !slices.Equal(b.Deps, a.Deps) {
		dropped, p := depsChange(b.Deps, a.Deps)
		if p != "" {
			c.problems = append(c.problems, p)
		} else {
			c.axioms = append(c.axioms, "the init functions of "+strings.Join(dropped, ", ")+" have no effect the program observes")
		}
	}
	return c
}

// registering packages: their init makes something visible through another
// package (image formats, hash and cipher registries, HTTP handlers, the
// embedded time zone database, gob type names), so dropping them is observable.
var registering = []string{"image/", "crypto", "net/http/pprof", "expvar", "time/tzdata", "encoding/gob", "runtime/", "database/sql", "embed", "plugin"}

// depsChange allows a change to drop standard-library packages whose init has
// no effect outside themselves; the dropped packages become an axiom.
func depsChange(b, a []string) (dropped []string, problem string) {
	in := map[string]bool{}
	for _, p := range a {
		in[p] = true
	}
	for _, p := range a {
		if !slices.Contains(b, p) {
			return nil, "the change imports a new package, " + p + ", whose init now runs"
		}
	}
	for _, p := range b {
		if in[p] {
			continue
		}
		if strings.Contains(strings.Split(p, "/")[0], ".") {
			return nil, "the change drops " + p + " from the build, and its init with it"
		}
		for _, r := range registering {
			if strings.HasPrefix(p, r) {
				return nil, "the change drops " + p + ", whose init registers something other packages observe"
			}
		}
		dropped = append(dropped, p)
	}
	return dropped, ""
}

// plainFunc says why fd could be observed by other means than a call: a
// method (method sets, interfaces), init and main, compiler directives.
func plainFunc(fd *ast.FuncDecl) string {
	switch {
	case fd.Recv != nil:
		return "a method, which changes a method set"
	case fd.Name.Name == "init" || fd.Name.Name == "main":
		return "which the runtime calls"
	case fd.Type.TypeParams != nil:
		return "a generic function (not modelled yet)"
	}
	if fd.Doc != nil {
		for _, c := range fd.Doc.List {
			if strings.HasPrefix(c.Text, "//go:") {
				return "which carries a compiler directive"
			}
		}
	}
	return ""
}

// decls are the package's types, constants and variables, canonically.
func (s *Snapshot) decls() map[string]string {
	out := map[string]string{}
	for _, f := range s.Files {
		for _, d := range f.Decls {
			g, ok := d.(*ast.GenDecl)
			if !ok || g.Tok == token.IMPORT {
				continue
			}
			for _, sp := range g.Specs {
				switch sp := sp.(type) {
				case *ast.TypeSpec:
					out["type "+sp.Name.Name] = s.canon(sp)
				case *ast.ValueSpec:
					var names []string
					for _, n := range sp.Names {
						names = append(names, n.Name)
					}
					out[g.Tok.String()+" "+strings.Join(names, ",")] = s.canon(sp)
				}
			}
		}
	}
	return out
}

func diffMaps(b, a map[string]string) string {
	var d []string
	for k, v := range b {
		if w, ok := a[k]; !ok {
			d = append(d, "removed "+k)
		} else if v != w {
			d = append(d, "changed "+k)
		}
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			d = append(d, "added "+k)
		}
	}
	sort.Strings(d)
	return strings.Join(d, ", ")
}

// initOrder: the package's variable initialisers in the order they run.
func (s *Snapshot) initOrder() []string {
	var out []string
	for _, in := range s.Info.InitOrder {
		var lhs []string
		for _, v := range in.Lhs {
			lhs = append(lhs, objKey(v))
		}
		out = append(out, strings.Join(lhs, ",")+"="+s.canon(in.Rhs))
	}
	return out
}
