package main

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dhilst/sx/internal/refactor"
)

// test/examples holds pairs: <name>_before.go is a program, <name>_after.go
// what the whole loop - detect, predict, apply, gate, keep or revert - makes
// of it. Each pair is one behaviour, isolated, so a change to any layer shows
// up as the examples it moves. `go test ./cmd/sx -run Examples -update`
// rewrites the after files from what the loop does now; review the diff.

var update = flag.Bool("update", false, "rewrite test/examples/*_after.go from the current output")

const buildIgnore = "//go:build ignore\n\n"

func TestExamples(t *testing.T) {
	for _, tool := range []string{"deadcode", "gopls", "eg"} {
		if _, ok := refactor.Tool(tool); !ok {
			t.Skipf("%s is not installed", tool)
		}
	}
	egDir, err := filepath.Abs("../../examples/eg")
	if err != nil {
		t.Fatal(err)
	}
	befores, err := filepath.Glob("../../test/examples/*_before.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(befores) == 0 {
		t.Fatal("no examples found")
	}
	for _, before := range befores {
		name := strings.TrimSuffix(filepath.Base(before), "_before.go")
		after := strings.TrimSuffix(before, "_before.go") + "_after.go"
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			src, err := os.ReadFile(before)
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example\n\ngo 1.25\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			main := filepath.Join(dir, "main.go")
			if err := os.WriteFile(main, bytes.TrimPrefix(src, []byte(buildIgnore)), 0o644); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := run([]string{"refactor", "-apply", "-n", "100", "-eg", egDir, dir}, &out, &out); err != nil {
				t.Fatalf("sx refactor: %v\n%s", err, out.String())
			}
			got, err := os.ReadFile(main)
			if err != nil {
				t.Fatal(err)
			}
			if *update {
				if err := os.WriteFile(after, append([]byte(buildIgnore), got...), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(after)
			if err != nil {
				t.Fatalf("no expected result: %v (run with -update to create it)", err)
			}
			if w := bytes.TrimPrefix(want, []byte(buildIgnore)); !bytes.Equal(got, w) {
				t.Errorf("sx made\n%s\nwant\n%s\nsx said:\n%s", got, w, out.String())
			}
		})
	}
}
