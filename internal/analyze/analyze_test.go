package analyze

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	gofront "purgatrix/internal/frontend/golang"
)

const guardSource = `package guard

func Walk(parts []string, limit int) int {
	total := 0
	for i := 0; i < len(parts); i++ {
		if i > limit {
			break
		} else if parts[i] == "." {
			if total > 3 {
				total += 2
			} else {
				total++
			}
			for j := 0; j < i; j++ {
				total += j
			}
		}
	}
	return total
}
`

// The same edit, written out by hand: the terminating arm keeps its if, and
// the other arm leaves the else.
const guardRewritten = `package guard

func Walk(parts []string, limit int) int {
	total := 0
	for i := 0; i < len(parts); i++ {
		if i > limit {
			break
		}
		if parts[i] == "." {
			if total > 3 {
				total += 2
			} else {
				total++
			}
			for j := 0; j < i; j++ {
				total += j
			}
		}
	}
	return total
}
`

func compileSource(t *testing.T, name, src string) (dir string) {
	t.Helper()
	dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func scoreOf(t *testing.T, name, src string) int {
	t.Helper()
	dir := compileSource(t, name, src)
	g, err := gofront.CompileFile(filepath.Join(dir, name), dir)
	if err != nil {
		t.Fatal(err)
	}
	total, err := Verify(g)
	if err != nil {
		t.Fatal(err)
	}
	return total
}

// The point of rewriting the graph is that the number stops being an estimate.
// This asserts the rewrite predicts exactly what the scorer charges once the
// equivalent source edit is made.
func TestExactSavingMatchesTheRealSourceEdit(t *testing.T) {
	dir := compileSource(t, "guard.go", guardSource)
	g, err := gofront.CompileFile(filepath.Join(dir, "guard.go"), dir)
	if err != nil {
		t.Fatal(err)
	}
	report, err := Analyze(g)
	if err != nil {
		t.Fatal(err)
	}
	var plan *Plan
	for i := range report.Plans {
		if report.Plans[i].Kind == "guard_inversion" {
			plan = &report.Plans[i]
			break
		}
	}
	if plan == nil {
		t.Fatalf("no guard_inversion plan found in %+v", report.Plans)
	}
	if !plan.Verified {
		t.Fatal("plan was not verified by a rewrite")
	}

	before := scoreOf(t, "guard.go", guardSource)
	after := scoreOf(t, "guard.go", guardRewritten)
	measured := before - after
	if plan.Exact != measured {
		t.Errorf("rewrite predicted %d, the source edit actually cost %d", plan.Exact, measured)
	}
	if plan.Exact <= 0 {
		t.Errorf("a guard inversion should lower the score, got %d", plan.Exact)
	}
}

func TestRewriteRejectsAnUnknownPlanKind(t *testing.T) {
	dir := compileSource(t, "guard.go", guardSource)
	g, err := gofront.CompileFile(filepath.Join(dir, "guard.go"), dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Rewrite(g, Plan{Kind: "invented"}); err == nil {
		t.Fatal("expected an unknown plan kind to be refused")
	}
	if _, err := Rewrite(g, Plan{Kind: "dead_state", NodeID: "nope"}); err == nil {
		t.Fatal("expected a missing node to be refused")
	}
}

// Every rewrite must leave a graph the scorer accepts; an invalid one is a bug
// in the rewrite, not a saving.
func TestEveryRewriteProducesAScorableGraph(t *testing.T) {
	dir := compileSource(t, "guard.go", guardSource)
	g, err := gofront.CompileFile(filepath.Join(dir, "guard.go"), dir)
	if err != nil {
		t.Fatal(err)
	}
	report, err := Analyze(g)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Plans) == 0 {
		t.Skip("no plans to rewrite")
	}
	for _, p := range report.Plans {
		rewritten, err := Rewrite(g, p)
		if err != nil {
			t.Errorf("%s at %s:%d: %v", p.Kind, p.Path, p.StartLine, err)
			continue
		}
		if _, err := Verify(rewritten); err != nil {
			t.Errorf("%s at %s:%d produced an unscorable graph: %v", p.Kind, p.Path, p.StartLine, err)
		}
	}
}

func TestDeadStateRewriteRemovesTheNodeAndItsEdges(t *testing.T) {
	dir := compileSource(t, "dead.go", "package dead\n\nfunc F() int {\n\tunused := 1\n\treturn 2\n}\n")
	g, err := gofront.CompileFile(filepath.Join(dir, "dead.go"), dir)
	if err != nil {
		t.Fatal(err)
	}
	var target string
	for _, n := range g.Nodes {
		if name, _ := n.Attributes["name"].(string); name == "unused" {
			target = n.ID
		}
	}
	if target == "" {
		t.Fatal("no state node for the unused local")
	}
	out, err := dropState(g, target)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range out.Nodes {
		if n.ID == target {
			t.Fatal("the state node survived the rewrite")
		}
	}
	for _, e := range out.Edges {
		if strings.Contains(e.From, target) || strings.Contains(e.To, target) {
			t.Fatal("an edge still points at the removed node")
		}
	}
}
