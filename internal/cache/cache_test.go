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

// Scoring must not require write access to the tree being scored.
func TestCompileGoFileSucceedsWhenCacheIsUnwritable(t *testing.T) {
	src := t.TempDir()
	path := filepath.Join(src, "a.go")
	if err := os.WriteFile(path, []byte("package a\n\nfunc F() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	readonly := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(readonly, 0o500); err != nil {
		t.Fatal(err)
	}
	c := New(filepath.Join(readonly, "sx-cache"))
	g, hit, err := c.CompileGoFile(path, src)
	if err != nil {
		t.Fatalf("compile failed when the cache was unwritable: %v", err)
	}
	if hit {
		t.Error("reported a cache hit from an unwritable cache")
	}
	if g == nil || len(g.Nodes) == 0 {
		t.Error("no graph produced")
	}
}
