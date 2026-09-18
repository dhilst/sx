package main

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPathListFlag(t *testing.T) {
	var paths pathListFlag
	if got := paths.Values([]string{"examples/eg", "sx/examples/eg"}); !reflect.DeepEqual(got, []string{"examples/eg", "sx/examples/eg"}) {
		t.Fatalf("defaults = %v", got)
	}
	if err := paths.Set("one,two" + string(os.PathListSeparator) + "three"); err != nil {
		t.Fatal(err)
	}
	if err := paths.Set("four"); err != nil {
		t.Fatal(err)
	}
	want := []string{"one", "two", "three", "four"}
	if got := paths.Values([]string{"unused"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}

	var disabled pathListFlag
	if err := disabled.Set(""); err != nil {
		t.Fatal(err)
	}
	if got := disabled.Values([]string{"examples/eg"}); len(got) != 0 {
		t.Fatalf("disabled paths = %v, want none", got)
	}
}

func TestGoFilesSkipsBuildIgnoredFiles(t *testing.T) {
	dir := t.TempDir()
	ordinary := filepath.Join(dir, "p.go")
	ignored := filepath.Join(dir, "ignored.go")
	if err := os.WriteFile(ordinary, []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ignored, []byte("//go:build ignore\n\npackage p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := goFiles(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{ordinary}) {
		t.Fatalf("files = %v, want only %s", got, ordinary)
	}
}

func TestRefactorCheckRejectsApply(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "p.go")
	orig := []byte("package p\n\nfunc unused() {}\n")
	if err := os.WriteFile(src, orig, 0o644); err != nil {
		t.Fatal(err)
	}
	err := run([]string{"refactor", "-check", "-apply", dir}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("err = %v, want -check/-apply rejection", err)
	}
	got, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, orig) {
		t.Fatalf("file changed:\n%s", got)
	}
}
