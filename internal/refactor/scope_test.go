package refactor

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// A module whose root directory is itself a package: pointed at a
// subdirectory, the scope is every package under it and everything that
// imports any of them - not just the one package in the directory.
func TestScopeCoversEveryPackageUnderThePath(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module m\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for pkg, src := range map[string]string{
		".":         "package main\n\nimport \"m/lib/a\"\n\nfunc main() { a.F() }\n",
		"lib":       "package lib\n\nfunc L() int { return 0 }\n",
		"lib/a":     "package a\n\nimport \"m/lib/a/b\"\n\nfunc F() int { return b.G() }\n",
		"lib/a/b":   "package b\n\nfunc G() int { return 1 }\n",
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
	s := TestScope(filepath.Join(root, "lib"))
	got := s.Packages()
	for _, want := range []string{"m/lib", "m/lib/a", "m/lib/a/b", "m"} {
		if !slices.Contains(got, want) {
			t.Errorf("scope %v is missing %s", got, want)
		}
	}
	if slices.Contains(got, "m/unrelated") {
		t.Errorf("scope %v includes a package that cannot be affected", got)
	}
	if s.Targets() != 3 {
		t.Errorf("%d packages under lib, want 3", s.Targets())
	}
	if all := TestScope(root).Packages(); len(all) != 1 || all[0] != "./..." {
		t.Errorf("the module root is the whole module, got %v", all)
	}
}

// A test that already fails is not the change's doing. It is recorded before
// the first change, skipped from then on, and only a new failure fails the
// gate.
func TestFailuresSeparatesBaselineFromNewFailures(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":      "module m\n\ngo 1.25\n",
		"p/p.go":      "package p\n\nfunc F() int { return 1 }\n",
		"p/p_test.go": "package p\n\nimport \"testing\"\n\nfunc TestBroken(t *testing.T) { t.Fatal(\"always\") }\n\nfunc TestF(t *testing.T) {\n\tif F() != 1 {\n\t\tt.Fatal(\"F\")\n\t}\n}\n",
	}
	for name, src := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	scope := Scope{root: root, pkgs: []string{"./..."}}
	baseline, err := scope.Failures(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !baseline["m/p\x00TestBroken"] || len(baseline) != 1 {
		t.Fatalf("baseline = %v, want only TestBroken", baseline)
	}
	again, err := scope.Failures(baseline)
	if err != nil {
		t.Fatal(err)
	}
	if err := again.Since(baseline); err != nil {
		t.Fatalf("nothing changed, but the gate failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "p/p.go"), []byte("package p\n\nfunc F() int { return 2 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	broken, err := scope.Failures(baseline)
	if err != nil {
		t.Fatal(err)
	}
	if err := broken.Since(baseline); err == nil || !strings.Contains(err.Error(), "TestF") {
		t.Fatalf("a change broke TestF, but the gate said %v", err)
	}
}

// A package that fails on its own leaves the gate; the rest of a ./... scope
// stays.
func TestWithoutDropsOnePackage(t *testing.T) {
	root := t.TempDir()
	for name, src := range map[string]string{
		"go.mod":        "module m\n\ngo 1.25\n",
		"stable/s.go":   "package stable\n",
		"unstable/u.go": "package unstable\n",
	} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := TestScope(root).Without("m/unstable").Packages()
	if !slices.Equal(got, []string{"m/stable"}) {
		t.Fatalf("scope = %v, want [m/stable]", got)
	}
}
