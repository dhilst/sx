package semhash

import (
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/*.golden from the current verdicts")

// module writes src as main.go of a one-file module and loads it.
func module(t *testing.T, src string) *Snapshot {
	t.Helper()
	dir := t.TempDir()
	src = strings.TrimPrefix(src, "//go:build ignore\n\n")
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// golden compares name -> verdict lines against testdata/<file>, or rewrites it.
func golden(t *testing.T, file string, got map[string]string) {
	t.Helper()
	var names []string
	for n := range got {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		b.WriteString(n + "\t" + got[n] + "\n")
	}
	path := filepath.Join("testdata", file)
	if *update {
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	wantMap := map[string]string{}
	for _, l := range strings.Split(strings.TrimSpace(string(want)), "\n") {
		if n, v, ok := strings.Cut(l, "\t"); ok {
			wantMap[n] = v
		}
	}
	for _, n := range names {
		if wantMap[n] != got[n] {
			t.Errorf("%s:\n  got  %s\n  want %s", n, got[n], wantMap[n])
		}
	}
}

// TestExamples proves every before/after pair of sx's example harness
// (cmd/sx/examples_test.go runs sx on each _before.go and expects _after.go).
func TestExamples(t *testing.T) {
	if testing.Short() {
		t.Skip("type-checks 56 pairs")
	}
	befores, _ := filepath.Glob("../../test/examples/*_before.go")
	if len(befores) == 0 {
		t.Fatal("no examples")
	}
	got := map[string]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	proved := 0
	for _, bf := range befores {
		name := strings.TrimSuffix(filepath.Base(bf), "_before.go")
		wg.Add(1)
		go func() {
			defer wg.Done()
			bsrc, _ := os.ReadFile(bf)
			asrc, _ := os.ReadFile(strings.TrimSuffix(bf, "_before.go") + "_after.go")
			v := Prove(module(t, string(bsrc)), module(t, string(asrc)), Options{AllowRemovedExported: true})
			mu.Lock()
			got[name] = v.String()
			if v.Proved {
				proved++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	t.Logf("%d/%d example pairs proved", proved, len(befores))
	golden(t, "examples.golden", got)
}

// TestDeterminism: evaluating a function twice, with fresh interners, builds
// the same term - no map order leaks into effects or their order.
func TestDeterminism(t *testing.T) {
	if testing.Short() {
		t.Skip("type-checks every example")
	}
	befores, _ := filepath.Glob("../../test/examples/*_before.go")
	for _, bf := range befores {
		bsrc, _ := os.ReadFile(bf)
		asrc, _ := os.ReadFile(strings.TrimSuffix(bf, "_before.go") + "_after.go")
		b, a := module(t, string(bsrc)), module(t, string(asrc))
		c := classify(b, a, Options{AllowRemovedExported: true})
		for _, k := range c.changed {
			render := func() string {
				defer func() { recover() }()
				in := newInterner(0)
				ev := newEvaluator(in, a, pick(c.after, c.added))
				return in.String(ev.function(c.after[k]))
			}
			for i := 0; i < 3; i++ {
				if x, y := render(), render(); x != y {
					t.Fatalf("%s %s: two evaluations built different terms", filepath.Base(bf), k)
				}
			}
		}
	}
}
