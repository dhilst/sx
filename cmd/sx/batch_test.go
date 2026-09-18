package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dhilst/sx/internal/refactor"
)

// A batched run commits each change, tests once at the end, bisects a
// failure to the change that caused it, drops it and detects again, and
// squashes the rest into one commit; sx status then reports the run. Only
// the inline of helperB breaks a test: the other three changes stay, whatever
// order they were made in.
func TestBatchBisectsAndDropsTheBreakingChange(t *testing.T) {
	if _, ok := refactor.Tool("gopls"); !ok {
		t.Skip("gopls is not installed")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module x\n\ngo 1.25\n",
		"main.go": `package main

import "fmt"

func helperA(n int) int { return n*2 + 1 }

func helperB(n int) int { return n*3 + 2 }

func first() {
	fmt.Println(helperA(3))
}

func second() {
	fmt.Println(helperB(4))
}

func main() {
	first()
	second()
}
`,
		"main_test.go": `package main

import (
	"os"
	"strings"
	"testing"
)

func TestHelperBStays(t *testing.T) {
	src, _ := os.ReadFile("main.go")
	if !strings.Contains(string(src), "func helperB") {
		t.Fatal("helperB is gone")
	}
}
`,
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-c", "commit.gpgsign=false", "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	git("init", "-q")
	git("add", "-A")
	git("commit", "-qm", "init")
	t.Setenv("GOFLAGS", "-count=1")

	var out bytes.Buffer
	if err := run([]string{"refactor", "-apply", "-batch", "-eg", "", dir}, &out, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	src, _ := os.ReadFile(filepath.Join(dir, "main.go"))
	if !strings.Contains(string(src), "func helperB") || strings.Contains(string(src), "func helperA") {
		t.Fatalf("want helperA inlined and helperB kept:\n%s\n%s", src, out.String())
	}
	if log := git("log", "--format=%s"); !strings.HasPrefix(log, "sx: 3 changes, 76 -> 45 nodes") || strings.Count(log, "\n") != 2 {
		t.Fatalf("want one squashed commit on top of init, got:\n%s", log)
	}
	if refs := git("for-each-ref", "refs/sx/runs"); refs == "" {
		t.Fatal("the separate commits were not kept")
	}
	var st bytes.Buffer
	if err := status(&st, dir); err != nil || !strings.Contains(st.String(), "RUN") || strings.Count(st.String(), "\n") != 2 {
		t.Fatalf("status: %v\n%s", err, st.String())
	}
}
