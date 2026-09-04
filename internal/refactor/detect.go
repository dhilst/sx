// Package refactor finds parts of a program that can be made smaller and
// applies the change, measured against min |AST|.
//
// Detection is ours; the transformations are not, wherever a maintained tool
// already does one. gopls inlines calls with type information, and deadcode
// answers reachability across a whole program. Both do it better than a
// syntactic pass could, and neither has any notion of which change is worth
// making.
package refactor

import (
	"bufio"
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

	"purgatrix/internal/cost"
)

// Kind names what a candidate proposes.
type Kind string

const (
	// KindDead removes a function nothing can reach.
	KindDead Kind = "dead"
	// KindInline replaces the single call to a function with its body, after
	// which the function itself is unreachable and goes too.
	KindInline Kind = "inline"
	// KindDuplicate factors repeated code into one function called from each
	// place the code used to be.
	KindDuplicate Kind = "dedup"
)

// Candidate is one change worth attempting, with what the measure says it
// should save.
type Candidate struct {
	Kind      Kind   `json:"kind"`
	File      string `json:"file"`
	Line      int    `json:"line"`
	Col       int    `json:"col"`
	Target    string `json:"target"`
	Predicted int    `json:"predicted"`
	Detail    string `json:"detail"`

	// Occurrences is set for a duplicate: every place the repeated code
	// appears, the first of which becomes the function.
	Occurrences []Occurrence `json:"occurrences,omitempty"`
	// Hash identifies the repeated run, so the remaining copies can be found
	// again after the first has been extracted and everything below it moved.
	Hash string `json:"-"`
}

// Key identifies a candidate, so a caller can remember which it has tried.
//
// A duplicate is keyed by what it is rather than where it is: every applied
// change moves the lines below it, so a position-based key lets a rejected
// candidate come back with a new name. One was retried seven times that way.
func (c Candidate) Key() string {
	if c.Kind == KindDuplicate {
		return string(c.Kind) + ":" + c.Hash
	}
	return fmt.Sprintf("%s:%s:%d:%d", c.Kind, c.File, c.Line, c.Col)
}

// Tool reports where a helper program is, if it is installed.
func Tool(name string) (string, bool) {
	if path, err := exec.LookPath(name); err == nil {
		return path, true
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidate := filepath.Join(home, "go", "bin", name)
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate, true
		}
	}
	return "", false
}

// DeadCandidates asks deadcode which functions the program cannot reach, and
// prices each one at the size of the declaration that would go.
//
// Reachability is a whole-program question - a function is live if anything
// calls it, including through an interface - so this is not something a
// file-at-a-time syntactic pass can answer honestly.
func DeadCandidates(deadcodePath, dir string) ([]Candidate, error) {
	cmd := exec.Command(deadcodePath, "./...")
	cmd.Dir = dir
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("deadcode: %w: %s", err, firstLine(errBuf.String()))
	}

	var cands []Candidate
	scanner := bufio.NewScanner(&out)
	for scanner.Scan() {
		file, line, name, ok := parseDeadcodeLine(scanner.Text(), dir)
		if !ok {
			continue
		}
		size, err := declSize(file, name)
		if err != nil || size == 0 {
			continue
		}
		cands = append(cands, Candidate{
			Kind: KindDead, File: file, Line: line, Target: name,
			Predicted: size,
			Detail:    fmt.Sprintf("%s is unreachable; its declaration is %d nodes", name, size),
		})
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].Predicted > cands[j].Predicted })
	return cands, nil
}

// parseDeadcodeLine reads "path:line:col: unreachable func: Name".
func parseDeadcodeLine(text, dir string) (file string, line int, name string, ok bool) {
	const marker = "unreachable func: "
	i := strings.Index(text, marker)
	if i < 0 {
		return "", 0, "", false
	}
	name = strings.TrimSpace(text[i+len(marker):])
	loc := strings.TrimSuffix(strings.TrimSpace(text[:i]), ":")
	parts := strings.Split(loc, ":")
	if len(parts) < 3 {
		return "", 0, "", false
	}
	line, err := strconv.Atoi(parts[len(parts)-2])
	if err != nil {
		return "", 0, "", false
	}
	file = strings.Join(parts[:len(parts)-2], ":")
	if !filepath.IsAbs(file) {
		file = filepath.Join(dir, file)
	}
	return file, line, name, true
}

// InlineCandidates finds functions called exactly once in the package.
//
// Under min |AST| a function used once always costs more than its body: the
// declaration, its parameter list, its returns and the call at the other end
// are all nodes that the inlined form does without. The saving is real but
// modest, and it is bounded by what gopls will actually agree to inline.
func InlineCandidates(dir string) ([]Candidate, error) {
	files, err := goFilesIn(dir)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	type decl struct {
		file  string
		line  int
		nodes int
		fn    *ast.FuncDecl
	}
	decls := map[string]decl{}
	calls := map[string][]token.Position{}

	for _, path := range files {
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			continue
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil || fn.Recv != nil {
				continue // methods can satisfy interfaces; leave them alone
			}
			if fn.Name.Name == "main" || fn.Name.Name == "init" || fn.Name.IsExported() {
				continue // entry points and API are reachable from outside
			}
			decls[fn.Name.Name] = decl{file: path, line: fset.Position(fn.Pos()).Line, nodes: cost.Count(fn), fn: fn}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if id, ok := call.Fun.(*ast.Ident); ok {
				calls[id.Name] = append(calls[id.Name], fset.Position(call.Lparen))
			}
			return true
		})
	}

	var cands []Candidate
	for name, d := range decls {
		sites := calls[name]
		if len(sites) != 1 {
			continue
		}
		// What is saved is the wrapper: the declaration and its signature,
		// less whatever the inliner has to add at the call site to preserve
		// behaviour. That last part is not predictable from the syntax - gopls
		// binds parameters to locals when they are used more than once, and
		// wraps the body in a closure when the call sits inside an expression -
		// so this figure orders the attempts and nothing more. The measurement
		// after the change is what decides.
		saving := d.nodes - cost.Count(d.fn.Body) + 1
		if saving <= 0 {
			continue
		}
		cands = append(cands, Candidate{
			Kind: KindInline, File: sites[0].Filename, Line: sites[0].Line, Col: sites[0].Column,
			Target: name, Predicted: saving,
			Detail: fmt.Sprintf("%s is called once; inlining drops the declaration and the call", name),
		})
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].Predicted > cands[j].Predicted })
	return cands, nil
}

// declSize is how many nodes a named function declaration takes.
func declSize(path, name string) (int, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return 0, err
	}
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && cost.FuncName(fn) == name || (ok && fn.Name.Name == name) {
			return cost.Count(fn), nil
		}
	}
	return 0, nil
}

func goFilesIn(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != dir && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			out = append(out, path)
		}
		return nil
	})
	return out, err
}

func skipDir(name string) bool {
	switch name {
	case "vendor", "testdata":
		return true
	}
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	if s == "" {
		return "no reason given"
	}
	return s
}
