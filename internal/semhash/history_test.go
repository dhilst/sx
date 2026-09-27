package semhash

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestHistory proves every change of an sx run kept behaviour: for each
// commit C in SX_PROVE_REPO's SX_PROVE_RANGE (a git revision range), the
// package SX_PROVE_PKG (default cmd/sx) at C^ against C.
func TestHistory(t *testing.T) {
	repo, rng := os.Getenv("SX_PROVE_REPO"), os.Getenv("SX_PROVE_RANGE")
	if repo == "" || rng == "" {
		t.Skip("set SX_PROVE_REPO and SX_PROVE_RANGE")
	}
	pkg := os.Getenv("SX_PROVE_PKG")
	if pkg == "" {
		pkg = "cmd/sx"
	}
	git := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}
	tree := map[string]*Snapshot{}
	load := func(rev string) *Snapshot {
		if s, ok := tree[rev]; ok {
			return s
		}
		dir := filepath.Join(t.TempDir(), rev)
		os.MkdirAll(dir, 0o755)
		archive := exec.Command("git", "-C", repo, "archive", rev)
		untar := exec.Command("tar", "-x", "-C", dir)
		untar.Stdin, _ = archive.StdoutPipe()
		untar.Start()
		if err := archive.Run(); err != nil {
			t.Fatal(err)
		}
		untar.Wait()
		s, err := Load(filepath.Join(dir, pkg))
		if err != nil {
			t.Fatalf("%s: %v", rev, err)
		}
		tree[rev] = s
		return s
	}
	commits := strings.Fields(git("rev-list", "--reverse", rng))
	proved := 0
	for i, c := range commits {
		subject := git("log", "-1", "--format=%s", c)
		v := Prove(load(c+"^"), load(c), Options{})
		if v.Proved {
			proved++
		}
		t.Logf("%2d %-60.60s %s", i+1, subject, v)
		if os.Getenv("SX_PROVE_DIFF") != "" && !v.Proved && strings.Contains(v.Reason, "normalise") {
			b, a := load(c+"^"), load(c)
			cl := classify(b, a, Options{})
			in := newInterner(0)
			for _, k := range cl.changed {
				eb := newEvaluator(in, b, pick(cl.before, cl.removed))
				ea := newEvaluator(in, a, pick(cl.after, cl.added))
				if tb, ta := eb.function(cl.before[k]), ea.function(cl.after[k]); tb != ta {
					d := in.firstDifference(tb, ta)
					if i := strings.Index(d, "\nBEFORE:"); i > 0 {
						d = d[:i] + "\n" + d[i:min(len(d), i+4000)]
					}
					path, rest, _ := strings.Cut(d, "\n")
					if len(path) > 400 {
						path = "..." + path[len(path)-400:]
					}
					t.Logf("   %s: %s\n%s", k, path, rest)
					for _, f := range strings.Fields(os.Getenv("SX_PROVE_NODES")) {
						n, _ := strconv.Atoi(f)
						t.Logf("NODE %d:\n%s", n, in.shallow(Node(n), 3))
					}
				}
			}
		}
	}
	t.Logf("%d/%d changes proved", proved, len(commits))
}
