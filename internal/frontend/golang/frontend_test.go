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

// The graph must not invent state that the program does not have, and must not
// lose reads that it does. Both directions mislead analysis over the IR.
func TestStateModellingHasNoPhantomsAndNoLostReads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "idx.go")
	src := `package idx

func Build(keys []string) map[string]bool {
	m := map[string]bool{}
	for _, k := range keys {
		m[k] = true
	}
	return m
}
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	g, err := CompileFile(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]map[string]int{}
	for _, n := range g.Nodes {
		if !sx.IsStateNode(n.Kind) {
			continue
		}
		name, _ := n.Attributes["name"].(string)
		names[name] = map[string]int{}
		for _, e := range g.Edges {
			if e.To == n.ID {
				names[name][e.Kind]++
			}
		}
	}
	for _, phantom := range []string{"string", "bool", "m[]", "m_"} {
		if _, ok := names[phantom]; ok {
			t.Errorf("graph invented a state node %q", phantom)
		}
	}
	if m, ok := names["m"]; !ok {
		t.Fatal("no state node for m")
	} else if m["write"] == 0 || m["read"] == 0 {
		t.Errorf("m should be both written and read, got %v", m)
	}
	if k, ok := names["k"]; !ok {
		t.Fatal("no state node for k")
	} else if k["read"] == 0 {
		t.Errorf("k is used as a map index and must be read, got %v", k)
	}
}

// A labelled statement is a jump target, not an opaque operation. Collapsing
// it hides its whole body from both the score and any analysis of the graph.
func TestLabelledStatementBodyIsCompiled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lbl.go")
	src := `package lbl

func F(pattern string) bool {
	found := false
Scan:
	for i := 0; i < len(pattern); i++ {
		if pattern[i] == 'x' {
			found = true
			break Scan
		}
	}
	return found
}
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	g, err := CompileFile(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, n := range g.Nodes {
		kinds[n.Kind]++
	}
	for _, want := range []string{"loop", "branch", "case", "jump"} {
		if kinds[want] == 0 {
			t.Errorf("no %s node inside the labelled loop: %v", want, kinds)
		}
	}
}
