package refactor

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/dhilst/sx/internal/cost"
)

// EgTemplates returns the example rewrite templates in paths.
func EgTemplates(paths []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, root := range paths {
		if root == "" {
			continue
		}
		info, err := os.Stat(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		if !info.IsDir() {
			if strings.HasSuffix(root, ".go") && !seen[root] {
				seen[root] = true
				out = append(out, root)
			}
			continue
		}
		err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if path != root && skipDir(d.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(path, ".go") && !seen[path] {
				seen[path] = true
				out = append(out, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}

// EgCandidates asks eg which templates match this tree, then prices each
// template by the expression nodes it removes per match.
func EgCandidates(egPath, dir string, templates []string) ([]Candidate, error) {
	var out []Candidate
	for _, tmpl := range templates {
		delta, err := egTemplateDelta(tmpl)
		if err != nil || delta <= 0 {
			continue
		}
		matches, firstFile, err := func() (int, string, error) {
			cmd := exec.Command(egPath, "-t", tmpl, "./...")
			cmd.Dir = dir
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				return 0, "", fmt.Errorf("eg %s: %w: %s", filepath.Base(tmpl), err, firstLine(stderr.String()))
			}
			return parseEgMatches(dir, stderr.String())
		}()
		if err != nil || matches == 0 {
			continue
		}
		name := strings.TrimSuffix(filepath.Base(tmpl), filepath.Ext(tmpl))
		out = append(out, Candidate{
			Kind: KindEg, File: firstFile, Target: name, Predicted: delta * matches,
			Template: tmpl,
			Detail:   fmt.Sprintf("%s matches %d expression(s); template saves about %d nodes each", filepath.Base(tmpl), matches, delta),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Predicted > out[j].Predicted })
	return out, nil
}

func parseEgMatches(dir, stderr string) (int, string, error) {
	total := 0
	firstFile := ""
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "===") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, "==="))
		open := strings.LastIndex(rest, "(")
		close := strings.LastIndex(rest, " matches)")
		if open < 0 || close < open {
			continue
		}
		file := strings.TrimSpace(rest[:open])
		n, err := strconv.Atoi(strings.TrimSpace(rest[open+1 : close]))
		if err != nil {
			continue
		}
		if !filepath.IsAbs(file) {
			file = filepath.Join(dir, file)
		}
		if firstFile == "" {
			firstFile = file
		}
		total += n
	}
	return total, firstFile, nil
}

func egTemplateDelta(path string) (int, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return 0, err
	}
	before, err := templateExpr(f, "before")
	if err != nil {
		return 0, err
	}
	after, err := templateExpr(f, "after")
	if err != nil {
		return 0, err
	}
	return cost.Count(before) - cost.Count(after), nil
}

func templateExpr(f *ast.File, name string) (ast.Node, error) {
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name.Name != name || fn.Body == nil || len(fn.Body.List) != 1 {
			continue
		}
		switch stmt := fn.Body.List[0].(type) {
		case *ast.ReturnStmt:
			if len(stmt.Results) == 1 {
				return stmt.Results[0], nil
			}
		case *ast.ExprStmt:
			return stmt.X, nil
		}
	}
	return nil, fmt.Errorf("no single-expression %s function in package %s", name, f.Name.Name)
}
