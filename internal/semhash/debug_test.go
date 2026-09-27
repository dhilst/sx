package semhash

import (
	"os"

	"github.com/dhilst/sx/internal/extract"
	"path/filepath"
	"strings"
	"testing"
)

func TestDebugOne(t *testing.T) {
	name := os.Getenv("SEMHASH_ONE")
	if name == "" {
		t.Skip()
	}
	bf := filepath.Join("../../test/examples", name+"_before.go")
	bsrc, _ := os.ReadFile(bf)
	asrc, _ := os.ReadFile(strings.TrimSuffix(bf, "_before.go") + "_after.go")
	b, a := module(t, string(bsrc)), module(t, string(asrc))
	t.Log(Prove(b, a, Options{AllowRemovedExported: true}))
	c := classify(b, a, Options{AllowRemovedExported: true})
	in := newInterner(0)
	for _, k := range c.changed {
		eb := newEvaluator(in, b, pick(c.before, c.removed))
		ea := newEvaluator(in, a, pick(c.after, c.added))
		tb, ta := eb.function(c.before[k]), ea.function(c.after[k])

		if tb != ta {
			t.Logf("%s:\n%s", k, in.firstDifference(tb, ta))
			if os.Getenv("SEMHASH_DUMP") != "" {
				t.Logf("BEFORE TERM:\n%s\nAFTER TERM:\n%s", in.String(tb), in.String(ta))
			}
		}
	}
}

func TestDebugCase(t *testing.T) {
	dir := os.Getenv("SEMHASH_CASE")
	if dir == "" {
		t.Skip()
	}
	src, _ := os.ReadFile(filepath.Join(dir, "main.go"))
	a, b := 0, 0
	for i, l := range strings.Split(string(src), "\n") {
		if strings.Contains(l, "//<") {
			a = i + 1
		}
		if strings.Contains(l, "//>") {
			b = i + 1
		}
	}
	work := t.TempDir()
	os.WriteFile(filepath.Join(work, "go.mod"), []byte("module c\n\ngo 1.25\n"), 0o644)
	os.WriteFile(filepath.Join(work, "main.go"), src, 0o644)
	res, err := extract.Extract(work, "main.go", a, b, extract.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("SEMHASH_SHOW") != "" {
		t.Log(string(res.Source))
	}
	bs, as := module(t, string(src)), module(t, string(res.Source))
	t.Log(Prove(bs, as, Options{}))
	c := classify(bs, as, Options{})
	in := newInterner(0)
	for _, k := range c.changed {
		eb := newEvaluator(in, bs, pick(c.before, c.removed))
		ea := newEvaluator(in, as, pick(c.after, c.added))
		tb, ta := eb.function(c.before[k]), ea.function(c.after[k])
		if tb != ta {
			t.Logf("%s:\n%s", k, in.firstDifference(tb, ta))
		}
	}
}

func TestDebugPair(t *testing.T) {
	dir := os.Getenv("SEMHASH_PAIR")
	if dir == "" {
		t.Skip()
	}
	bsrc, _ := os.ReadFile(filepath.Join(dir, "before.go"))
	asrc, _ := os.ReadFile(filepath.Join(dir, "after.go"))
	bs, as := module(t, string(bsrc)), module(t, string(asrc))
	t.Log(Prove(bs, as, Options{}))
	c := classify(bs, as, Options{})
	in := newInterner(0)
	for _, k := range c.changed {
		eb := newEvaluator(in, bs, pick(c.before, c.removed))
		ea := newEvaluator(in, as, pick(c.after, c.added))
		tb, ta := eb.function(c.before[k]), ea.function(c.after[k])
		if tb != ta {
			t.Logf("%s:\n%s", k, in.firstDifference(tb, ta))
		}
	}
}
