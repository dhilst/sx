package semhash

import (
	"os"
	"path/filepath"
	"testing"
)

// TestNegative: changes that alter behaviour must never be proved. This is
// the soundness guard: a failure here is a wrong proof, not a missed one.
func TestNegative(t *testing.T) {
	if testing.Short() {
		t.Skip("type-checks every pair")
	}
	for name, v := range provePairs(t, "neg") {
		if v.Proved {
			t.Errorf("%s: PROVED a behaviour change: %s", name, v)
		} else {
			t.Logf("%-24s %s", name, v.Reason)
		}
	}
}

// TestPositive: changes that keep behaviour and that the normaliser is meant
// to see through.
func TestPositive(t *testing.T) {
	if testing.Short() {
		t.Skip("type-checks every pair")
	}
	for name, v := range provePairs(t, "pos") {
		if !v.Proved {
			t.Errorf("%s: %s", name, v)
		}
	}
}

func provePairs(t *testing.T, dir string) map[string]Verdict {
	t.Helper()
	dirs, _ := filepath.Glob(filepath.Join("testdata", dir, "*"))
	if len(dirs) == 0 {
		t.Fatalf("no pairs in testdata/%s", dir)
	}
	out := map[string]Verdict{}
	for _, d := range dirs {
		b, _ := os.ReadFile(filepath.Join(d, "before.go"))
		a, _ := os.ReadFile(filepath.Join(d, "after.go"))
		out[filepath.Base(d)] = Prove(module(t, string(b)), module(t, string(a)), Options{})
	}
	return out
}
