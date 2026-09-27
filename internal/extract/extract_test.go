package extract

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// markers finds the //< and //> lines of a case: the range to extract.
func markers(t *testing.T, src []byte) (int, int) {
	a, b := 0, 0
	for i, l := range strings.Split(string(src), "\n") {
		if strings.Contains(l, "//<") {
			a = i + 1
		}
		if strings.Contains(l, "//>") {
			b = i + 1
		}
	}
	if a == 0 || b == 0 {
		t.Fatal("no //< ... //> markers")
	}
	return a, b
}

func copyDir(t *testing.T, from string) string {
	to := t.TempDir()
	err := filepath.WalkDir(from, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, path)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(to, rel), 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(to, rel), data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return to
}

func goCmd(t *testing.T, dir string, args ...string) string {
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, out.String())
	}
	return out.String()
}

func needGo(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs every case")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not installed")
	}
}

// TestCases: every program in testdata/cases prints the same after its
// //< ... //> range is extracted, and the result passes go vet.
func TestCases(t *testing.T) {
	needGo(t)
	dirs, err := filepath.Glob("testdata/cases/*")
	if err != nil || len(dirs) == 0 {
		t.Fatal("no cases")
	}
	for _, dir := range dirs {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			t.Parallel()
			src, err := os.ReadFile(filepath.Join(dir, "main.go"))
			if err != nil {
				t.Fatal(err)
			}
			a, b := markers(t, src)
			want := goCmd(t, dir, "run", ".")
			work := copyDir(t, dir)
			res, err := Extract(work, "main.go", a, b, Options{})
			if err != nil {
				t.Fatalf("%v\n  %s", err, strings.Join(res.Log, "\n  "))
			}
			if err := os.WriteFile(filepath.Join(work, "main.go"), res.Source, 0o644); err != nil {
				t.Fatal(err)
			}
			goCmd(t, work, "vet", ".")
			if got := goCmd(t, work, "run", "."); got != want {
				t.Errorf("output changed\nbefore:\n%s\nafter:\n%s\nlog:\n  %s\nsource:\n%s", want, got, strings.Join(res.Log, "\n  "), res.Source)
			}
		})
	}
}

// TestSxRun extracts ranges of sx's own run(), frozen in testdata/sx-run at
// the commit in its SNAPSHOT file: the defers, the goto into a labelled loop,
// the pending revert shared with a goroutine. Each result must build and vet.
// (Their behaviour was checked with cmd/sx's test suite when the snapshot was
// taken; that suite needs test fixtures and tools not kept here.)
func TestSxRun(t *testing.T) {
	needGo(t)
	ranges := []struct {
		a, b int
		uses string // an adapter the extraction must use, if any
	}{
		{42, 441, "rename param"},         // the refactor subcommand, in tail position
		{66, 76, "registered with the"},   // profiling: two defers not in tail position
		{89, 100, "signal"},               // a search loop that returns
		{125, 135, "registered with the"}, // the LSP session and its deferred close
		{150, 170, "share it"},            // baseline bookkeeping
		{176, 215, "share it"},            // interrupt handling and the pending revert
		{219, 432, "signal"},              // the batched loop body
		{240, 260, "break"},               // an inner candidate loop
		{340, 375, ""},                    // failure accounting
		{415, 432, "goto batched"},        // bisect, then goto batched
	}
	for _, r := range ranges {
		t.Run(fmt.Sprintf("%d-%d", r.a, r.b), func(t *testing.T) {
			t.Parallel()
			work := copyDir(t, "testdata/sx-run")
			res, err := Extract(filepath.Join(work, "cmd/sx"), "main.go", r.a, r.b, Options{})
			if err != nil {
				t.Fatalf("%v\n  %s", err, strings.Join(res.Log, "\n  "))
			}
			if !strings.Contains(strings.Join(res.Log, "\n"), r.uses) {
				t.Errorf("expected the log to mention %q:\n  %s", r.uses, strings.Join(res.Log, "\n  "))
			}
			if err := os.WriteFile(filepath.Join(work, "cmd/sx/main.go"), res.Source, 0o644); err != nil {
				t.Fatal(err)
			}
			goCmd(t, work, "vet", "./cmd/sx")
		})
	}
}

// TestRefusals: ranges with nothing to extract are refused with a reason.
func TestRefusals(t *testing.T) {
	needGo(t)
	dir := t.TempDir()
	src := `package main

func main() {
	x := 1
	// only a comment
	// and another
	println(x)
}

func one() {
	println("the whole body")
}
`
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module c\n\ngo 1.25\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644)
	for _, c := range []struct {
		a, b int
		want error
	}{
		{5, 6, ErrNoCode},
		{11, 11, ErrWholeBody},
	} {
		if _, err := Extract(dir, "main.go", c.a, c.b, Options{}); !errors.Is(err, c.want) {
			t.Errorf("lines %d-%d: got %v, want %v", c.a, c.b, err, c.want)
		}
	}
}

// TestGrowthModes: with the adapters off, jumps and defers grow R instead;
// both modes must keep the program's output.
func TestGrowthModes(t *testing.T) {
	needGo(t)
	for _, name := range []string{"28-mixed-signals", "32-defer-order"} {
		dir := filepath.Join("testdata/cases", name)
		src, _ := os.ReadFile(filepath.Join(dir, "main.go"))
		a, b := markers(t, src)
		want := goCmd(t, dir, "run", ".")
		for _, opt := range []Options{{GrowJumps: true}, {GrowDefers: true}} {
			work := copyDir(t, dir)
			res, err := Extract(work, "main.go", a, b, opt)
			if errors.Is(err, ErrGrewWholeBody) {
				continue // growing all the way is a correct answer too
			}
			if err != nil {
				t.Fatalf("%s %+v: %v", name, opt, err)
			}
			os.WriteFile(filepath.Join(work, "main.go"), res.Source, 0o644)
			if got := goCmd(t, work, "run", "."); got != want {
				t.Errorf("%s %+v: output changed:\n%s\nwant:\n%s", name, opt, got, want)
			}
		}
	}
}
