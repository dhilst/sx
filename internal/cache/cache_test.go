package cache

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCompileGoFileReusesUnchangedTranslation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.go")
	if err := os.WriteFile(path, []byte("package sample\n\nfunc f() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := New(filepath.Join(dir, ".cache"))
	if _, hit, err := c.CompileGoFile(path, dir); err != nil {
		t.Fatal(err)
	} else if hit {
		t.Fatal("first compile unexpectedly hit cache")
	}
	if _, hit, err := c.CompileGoFile(path, dir); err != nil {
		t.Fatal(err)
	} else if !hit {
		t.Fatal("second compile did not hit cache")
	}
	if err := os.WriteFile(path, []byte("package sample\n\nfunc f() { println(1) }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, hit, err := c.CompileGoFile(path, dir); err != nil {
		t.Fatal(err)
	} else if hit {
		t.Fatal("changed source unexpectedly hit old cache entry")
	}
}
