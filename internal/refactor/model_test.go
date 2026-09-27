package refactor

import (
	"bytes"
	"go/ast"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/dhilst/sx/internal/cost"
)

// The models are checked against the tools they predict. Every candidate the
// detectors find in test/examples - including the ones the model says would
// grow the tree - is applied for real, and the measured change has to be the
// predicted one. A model that is right only about the candidates it likes
// cannot be told apart from one that is lucky.

// exampleDir is where the before/after library lives.
const exampleDir = "../../test/examples"

// examples lists the example names, optionally only those with a prefix.
func examples(t *testing.T, prefix string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(exampleDir, prefix+"*_before.go"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range paths {
		names = append(names, strings.TrimSuffix(filepath.Base(p), "_before.go"))
	}
	sort.Strings(names)
	return names
}

// exampleSource is an example file without the build constraint that keeps
// it out of this module's build.
func exampleSource(t *testing.T, name string) string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(exampleDir, name))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimPrefix(string(src), "//go:build ignore\n\n")
}

// exampleModule writes an example into a fresh module as main.go.
func exampleModule(t *testing.T, name string) string {
	t.Helper()
	return write(t, "main.go", exampleSource(t, name+"_before.go"))
}

func treeNodes(t *testing.T, dir string) int {
	t.Helper()
	return treeScore(t, dir).Nodes
}

func treeScore(t *testing.T, dir string) cost.Tree {
	t.Helper()
	files, err := goFilesIn(dir)
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	var weights []int
	for _, f := range files {
		scored, err := cost.ScoreFile(f)
		if err != nil {
			t.Fatal(err)
		}
		total += scored.Nodes
		weights = append(weights, scored.Weights()...)
	}
	return cost.Tree{Nodes: total, Objective: cost.Objective(total, weights)}
}

func requireTools(t *testing.T, names ...string) map[string]string {
	t.Helper()
	paths := map[string]string{}
	for _, name := range names {
		path, ok := Tool(name)
		if !ok {
			t.Skipf("%s is not installed", name)
		}
		paths[name] = path
	}
	return paths
}

// measure applies c in a fresh copy of the example and reports the change in
// |AST| and whether the result builds.
func measure(t *testing.T, name string, find func(dir string) (Candidate, bool), tools map[string]string) (delta int, builds bool, after string, err error) {
	t.Helper()
	d, _, builds, after, err := measureObjective(t, name, find, tools)
	return d, builds, after, err
}

// measureObjective is measure with the change in J too.
func measureObjective(t *testing.T, name string, find func(dir string) (Candidate, bool), tools map[string]string) (delta int, dj float64, builds bool, after string, err error) {
	t.Helper()
	dir := exampleModule(t, name)
	c, ok := find(dir)
	if !ok {
		t.Fatalf("the candidate is not found again in a fresh copy")
	}
	before := treeScore(t, dir)
	snapshot, err := Stamp(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(dir, c, tools["gopls"], tools["eg"]); err != nil {
		return 0, 0, false, "", err
	}
	if err := Format(dir, snapshot); err != nil {
		t.Fatal(err)
	}
	builds, _ = Repair(dir)
	src, _ := os.ReadFile(filepath.Join(dir, "main.go"))
	now := treeScore(t, dir)
	return now.Nodes - before.Nodes, now.Objective - before.Objective, builds, string(src), nil
}

// kinds is how each transformation's candidates are found in an example,
// all of them, including the ones the model says would grow the tree.
func kinds(t *testing.T) map[string]func(dir string) ([]Candidate, error) {
	tools := map[string]string{}
	for _, name := range []string{"deadcode", "gopls", "eg"} {
		if path, ok := Tool(name); ok {
			tools[name] = path
		}
	}
	need := func(name string) {
		if tools[name] == "" {
			t.Skipf("%s is not installed", name)
		}
	}
	return map[string]func(string) ([]Candidate, error){
		"dedup_": func(dir string) ([]Candidate, error) {
			need("gopls")
			return duplicates(dir)
		},
		"heuristic_": func(dir string) ([]Candidate, error) {
			need("gopls")
			return NewCache().Heuristics(dir)
		},
		"extract_": func(dir string) ([]Candidate, error) {
			need("gopls")
			return NewCache().Extractions(dir)
		},
		"inline_": func(dir string) ([]Candidate, error) {
			need("gopls")
			return inlines(dir)
		},
		"dead_": func(dir string) ([]Candidate, error) {
			need("deadcode")
			return DeadCandidates(tools["deadcode"], dir)
		},
		"eg_": func(dir string) ([]Candidate, error) {
			need("eg")
			templates, err := EgTemplates([]string{"../../examples/eg"})
			if err != nil {
				return nil, err
			}
			for i, tmpl := range templates {
				if abs, err := filepath.Abs(tmpl); err == nil {
					templates[i] = abs
				}
			}
			return NewCache().egRewrites(dir, templates)
		},
	}
}

// identity is what names a candidate across two copies of one example.
func identity(c Candidate) string {
	return string(c.Kind) + "|" + c.Target + "|" + c.Hash + "|" + c.Template
}

// Every model is checked against the tool it predicts: each candidate found
// in an example is applied in a fresh copy, and the measured change in |AST|
// has to be the predicted one, exactly.
func TestModelsMatchReality(t *testing.T) {
	tools := map[string]string{}
	for _, name := range []string{"gopls", "eg"} {
		tools[name], _ = Tool(name)
	}
	for prefix, find := range kinds(t) {
		for _, name := range examples(t, prefix) {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				all, err := find(exampleModule(t, name))
				if err != nil {
					t.Fatal(err)
				}
				for _, c := range all {
					t.Run(c.Target, func(t *testing.T) {
						got, dj, builds, after, err := measureObjective(t, name, func(dir string) (Candidate, bool) {
							again, err := find(dir)
							if err != nil {
								t.Fatal(err)
							}
							for _, d := range again {
								if identity(d) == identity(c) {
									return d, true
								}
							}
							return Candidate{}, false
						}, tools)
						if err != nil {
							t.Fatalf("predicted %s, but the change failed: %v", c.model, err)
						}
						if got != c.model.Delta() {
							t.Errorf("predicted ΔN=%+d, measured %+d\n  model: %s\n%s", c.model.Delta(), got, c.model, after)
						}
						if math.Abs(dj+c.Gain) > 1e-9 {
							t.Errorf("predicted ΔJ=%+.3f, measured %+.3f\n  model: %s\n%s", -c.Gain, dj, c.model, after)
						}
						if !builds {
							t.Errorf("the model priced a change that does not build (%s):\n%s", c.model, after)
						}
					})
				}
			})
		}
	}
}

// Runs the model refuses, each marked by a line it must not offer a run
// containing - unless the run also contains what makes it safe.
func TestExtractionModelRefuses(t *testing.T) {
	for name, marker := range map[string][2]string{
		"dedup_address_taken":  {"cmd.Stdout = &out", "var out"},
		"dedup_missing_import": {"f, err := parser.ParseFile", ""},
		"dedup_free_branch":    {"continue", ""},
		"dedup_loop_carried":   {"prev = x", "prev := 0"},
		"dedup_generic":        {"first", ""},
		"dedup_types_differ":   {"opts.token", ""},
		"dedup_defer":          {"defer mu.Unlock()", ""},
		"dedup_redeclared":     {"n, err := strconv.Atoi(s)", ""},
		"dedup_returns_differ": {"return nil, err", ""},
		"dedup_results_differ": {"body := strings.TrimSpace(s)", ""},
		"dedup_field_write":    {"s.count = len(xs)", "stats{"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := exampleModule(t, name)
			src := exampleSource(t, name+"_before.go")
			all, err := duplicates(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range all {
				o := c.Occurrences[0]
				run := src[o.StartOffset:o.EndOffset]
				if strings.Contains(run, marker[0]) && (marker[1] == "" || !strings.Contains(run, marker[1])) {
					t.Errorf("%s offered a run containing %q: %s", c.Target, marker[0], c.Detail)
				}
			}
		})
	}
}

// goBuild reports whether the module in dir builds.
func goBuild(dir string) bool {
	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	return cmd.Run() == nil
}

// Every run of the example that contains a nested "return total, ..." but
// not total's declaration is refused, unless the call is simply returned:
// the call gopls writes would declare its carried-back value over total with
// :=, and the path that does not return would zero it.
func TestExtractionRefusesReturningAnOuterLocal(t *testing.T) {
	dir := exampleModule(t, "extract_return_local")
	src := exampleSource(t, "extract_return_local_before.go")
	c := NewCache()
	c.Begin(dir)
	tp, err := c.load(dir)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, f := range tp.files {
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			lists(fn.Body, func(list []ast.Stmt) {
				for i := range list {
					for j := i + 1; j <= len(list); j++ {
						run := src[tp.fset.Position(list[i].Pos()).Offset:tp.fset.Position(list[j-1].End()).Offset]
						if !strings.Contains(run, "return total, errors.New") || strings.Contains(run, "total := 0") {
							continue
						}
						checked++
						m, err := predictExtraction(tp, f, list[i:j], 1, []*ast.File{f})
						if err == nil && !strings.HasPrefix(m.Control, "trailing") {
							t.Errorf("priced a run returning total: %s\n%s", m, run)
						}
					}
				}
			})
		}
	}
	if checked == 0 {
		t.Fatal("no run contains the return")
	}
}
