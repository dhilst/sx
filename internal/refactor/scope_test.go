package refactor

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// A change to a package can break any package that imports it, and nothing in
// the changed package's own tests will say so. Refactoring
// internal/runtime/maps passed its own suite - good enough to reject six other
// inlines in the same run - and left reflect corrupting type descriptors.
func TestScopeIncludesImporters(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module m\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for pkg, src := range map[string]string{
		"leaf":      "package leaf\n\nfunc F() int { return 1 }\n",
		"mid":       "package mid\n\nimport \"m/leaf\"\n\nfunc G() int { return leaf.F() }\n",
		"far":       "package far\n\nimport \"m/mid\"\n\nfunc H() int { return mid.G() }\n",
		"unrelated": "package unrelated\n\nfunc I() int { return 3 }\n",
	} {
		d := filepath.Join(root, pkg)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "p.go"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got := TestScope(filepath.Join(root, "leaf")).Packages()
	for _, want := range []string{"m/leaf", "m/mid", "m/far"} {
		if !slices.Contains(got, want) {
			t.Errorf("scope %v is missing %s, which a change to leaf can break", got, want)
		}
	}
	if slices.Contains(got, "m/unrelated") {
		t.Errorf("scope %v includes a package that cannot be affected", got)
	}
}

// Pointed at a whole repository rather than one package, the scope is the
// repository: that is what ./... already meant, and it is already right.
func TestScopeOfAWholeTreeIsTheWholeTree(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module m\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := filepath.Join(root, "sub")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "p.go"), []byte("package sub\n\nfunc F() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := TestScope(root).Packages()
	if len(got) != 1 || got[0] != "./..." {
		t.Fatalf("a directory that is not itself a package should fall back to ./..., got %v", got)
	}
}
