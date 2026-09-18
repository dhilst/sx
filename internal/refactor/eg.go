package refactor

import (
	"bytes"
	"fmt"
	"go/ast"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
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

// EgCandidates asks eg which templates match this tree, and has the model
// price each one over every match.
func EgCandidates(egPath, dir string, templates []string) ([]Candidate, error) {
	all, err := egRewrites(egPath, dir, templates)
	if err != nil {
		return nil, err
	}
	var out []Candidate
	for _, c := range all {
		if c.Predicted > 0 {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Predicted > out[j].Predicted })
	return out, nil
}

// egRewrites is every template eg finds a match for, priced by the model.
func egRewrites(egPath, dir string, templates []string) ([]Candidate, error) {
	var out []Candidate
	ps := packages{}
	for _, tmpl := range templates {
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
		model, err := predictEg(ps, dir, tmpl)
		if err != nil {
			continue
		}
		name := strings.TrimSuffix(filepath.Base(tmpl), filepath.Ext(tmpl))
		out = append(out, Candidate{
			Kind: KindEg, File: firstFile, Target: name, Predicted: -model.Delta(),
			Template: tmpl, model: model,
			Detail: fmt.Sprintf("%s matches %d expression(s): %s", filepath.Base(tmpl), matches, model),
		})
	}
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
