package golang

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"purgatrix/internal/sx"
)

func TestCompileFileDeterministic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.go")
	source := `package sample

func add(a, b int) int {
	if a > b {
		return a + b
	}
	return b
}
`
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := CompileFile(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CompileFile(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	var a, b bytes.Buffer
	if err := sx.Encode(&a, first); err != nil {
		t.Fatal(err)
	}
	if err := sx.Encode(&b, second); err != nil {
		t.Fatal(err)
	}
	if a.String() != b.String() {
		t.Fatalf("frontend output is not deterministic\n%s\n---\n%s", a.String(), b.String())
	}
}

func TestPackageIDDistinguishesSameNamedPackages(t *testing.T) {
	// Two `main` packages in different directories are different packages.
	// Identifying them by package name alone collides on merge.
	if a, b := packageID("cmd/sx/main.go", "main"), packageID("cmd/sxga/main.go", "main"); a == b {
		t.Fatalf("packageID collided for distinct packages: %q", a)
	}
	if got := packageID("main.go", "main"); got != "main" {
		t.Errorf("packageID at module root = %q, want the package name", got)
	}
}

func TestCompileMergeAcceptsSameNamedPackagesInDifferentDirs(t *testing.T) {
	dir := t.TempDir()
	var graphs []*sx.Graph
	for _, sub := range []string{"cmd/one", "cmd/two"} {
		path := filepath.Join(dir, sub, "main.go")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("package main\n\nfunc main() { println(1) }\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		g, err := CompileFile(path, dir)
		if err != nil {
			t.Fatal(err)
		}
		graphs = append(graphs, g)
	}
	if _, err := sx.Merge(graphs...); err != nil {
		t.Fatalf("merging two main packages failed: %v", err)
	}
}
