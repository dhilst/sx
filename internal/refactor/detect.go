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
	"go/build"
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
	switch c.Kind {
	case KindDuplicate:
		// Content, because the copies move whenever anything above them does.
		return string(c.Kind) + ":" + c.Hash
	case KindInline:
		// The function, not the call site. Keying on the position meant that
		// any accepted change below it renamed the key, so a candidate already
		// rejected came back: three of them were retried three times each in a
		// single pass, at three and a half seconds a go.
		return fmt.Sprintf("%s:%s:%s", c.Kind, filepath.Dir(c.File), c.Target)
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
		file, line, name, ok := func(text string) (file string, line int, name string, ok bool) {
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
		}(scanner.Text())
		if !ok {
			continue
		}
		size, err := func() (int, error) {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, file, nil, 0)
			if err != nil {
				return 0, err
			}
			for _, d := range f.Decls {
				fn, ok := d.(*ast.FuncDecl)
				if !ok || fn.Recv != nil {
					// A method cannot be removed by name: the removal matches
					// plain functions, so proposing one only fails later.
					continue
				}
				if cost.FuncName(fn) == name || fn.Name.Name == name {
					return cost.Count(fn), nil
				}
			}
			return 0, nil
		}()
		if err != nil || size == 0 {
			continue
		}
		if generated(file) {
			continue // unreachable, but rewriting it would be undone anyway
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
	// Everything is keyed by directory as well as name, because an unexported
	// function belongs to its package. A bare name conflated same-named
	// helpers in different packages, and the platform variants of one name in
	// a single package - syscall declares fcntl in six files - and then the
	// removal took whichever declaration happened to be parsed last. On this
	// host that breaks nothing visible; on another platform it deletes a
	// function that is still called.
	decls := map[string]decl{}
	declared := map[string]int{}
	calls := map[string][]token.Position{}
	// References, not just calls. A function can be named without being
	// called - `var ParseFoo = parseFoo` in an export_test.go is the standard
	// way the standard library reaches its own unexported code - and that
	// reference keeps it alive. Counting calls alone offered five such
	// candidates in internal/runtime/cgroup, each costing a gopls round trip
	// before the removal was refused.
	refs := map[string]int{}
	key := func(path, name string) string { return filepath.Dir(path) + "\x00" + name }

	// A test is a caller. Counting only the files that ship makes a helper the
	// tests cover look called once, and inlining it deletes a declaration the
	// tests still name - so the build fails, the change is reverted, and the
	// same candidate is offered again on the next pass. Three of them cost
	// about ten seconds a pass here before the tests were counted.
	tests, err := testFilesIn(dir)
	if err != nil {
		return nil, err
	}
	// Line ranges of functions carrying a compiler directive, per file. A
	// //go:nosplit function has a fixed stack budget and no split check;
	// moving a body into it is the one thing the directive forbids. internal/
	// runtime/atomic has exactly this: panicUnaligned called once, from
	// lockAndCheck, which is //go:nosplit.
	directed := map[string][][2]int{}
	for _, path := range append(append([]string{}, files...), tests...) {
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			continue
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || !hasDirective(fn.Doc) {
				continue
			}
			directed[path] = append(directed[path],
				[2]int{fset.Position(fn.Pos()).Line, fset.Position(fn.End()).Line})
		}
		// A test file contributes call sites, not candidates, and so does a
		// generated one: it can be read, but writing to it is undone the next
		// time the generator runs.
		if !strings.HasSuffix(path, "_test.go") && !generated(path) && inCurrentBuild(path) {
			for _, d := range f.Decls {
				fn, ok := d.(*ast.FuncDecl)
				if !ok || fn.Body == nil || fn.Recv != nil {
					continue // methods can satisfy interfaces; leave them alone
				}
				if fn.Name.Name == "main" || fn.Name.Name == "init" || fn.Name.IsExported() {
					continue // entry points and API are reachable from outside
				}
				if func() bool {
					found := false
					ast.Inspect(fn, func(n ast.Node) bool {
						if id, ok := n.(*ast.Ident); ok && id.Name == "unsafe" {
							found = true
						}
						return true
					})
					return found
				}() {
					// Moving a body that uses unsafe puts it in a different
					// frame, and what unsafe.Pointer guarantees depends on
					// where the thing it points at lives and how long it stays
					// alive. reflectlite.packEface is the case: built around
					// unsafe.Pointer(&i), with a comment saying its
					// correctness depends on no operation coming between two
					// assignments, because the collector must not see the
					// half-built interface. Neither the build nor the tests can
					// see a change to that, so it is not attempted.
					continue
				}
				if hasDirective(fn.Doc) {
					// The directive is the point of the declaration. Inlining
					// the body discards it, and removing the function takes it
					// with the doc comment.
					continue
				}
				k := key(path, fn.Name.Name)
				declared[k]++
				decls[k] = decl{file: path, line: fset.Position(fn.Pos()).Line, nodes: cost.Count(fn), fn: fn}
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CallExpr:
				if id, ok := x.Fun.(*ast.Ident); ok {
					calls[key(path, id.Name)] = append(calls[key(path, id.Name)], fset.Position(x.Lparen))
				}
			case *ast.Ident:
				refs[key(path, x.Name)]++
			}
			return true
		})
	}

	var cands []Candidate
	for k, d := range decls {
		name := k[strings.IndexByte(k, 0)+1:]
		if declared[k] > 1 {
			// Several files declare it, one per platform. Which one the call
			// resolves to is a build-tag question, not a syntactic one.
			continue
		}
		sites := calls[k]
		if len(sites) != 1 {
			continue
		}
		// Two mentions: the declaration's own name, and the one call. A third
		// is something else holding on to it.
		if refs[k] != 2 {
			continue
		}
		if generated(sites[0].Filename) {
			continue // the one call site is in a file that must not be edited
		}
		if strings.HasSuffix(sites[0].Filename, "_test.go") {
			// The measure does not count tests. Inlining into one and removing
			// the declaration moves the body out of what is measured rather
			// than removing it, and the measure records the whole thing as a
			// saving: 29 nodes to 12 on a two-function package, with nothing
			// deleted. Anything the objective ignores is somewhere it can hide
			// code, so the change has to land where the measure can see it.
			continue
		}
		if func() bool {
			var line int = sites[0].Line
			for _, r := range directed[sites[0].Filename] {
				if line >= r[0] && line <= r[1] {
					return true
				}
			}
			return false
		}() {
			continue // the call sits inside a function a directive constrains
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

// testFilesIn is the tests, read for the call sites they contain and for what
// they still name after a declaration is removed.
func testFilesIn(dir string) ([]string, error) {
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
		if strings.HasSuffix(path, "_test.go") {
			out = append(out, path)
		}
		return nil
	})
	return out, err
}

// hasDirective reports whether a doc comment carries a compiler directive.
// //go:generate is excluded: it instructs a tool, not the compiler.
func hasDirective(doc *ast.CommentGroup) bool {
	if doc == nil {
		return false
	}
	for _, c := range doc.List {
		if strings.HasPrefix(c.Text, "//go:") && !strings.HasPrefix(c.Text, "//go:generate") {
			return true
		}
	}
	return false
}

// generated reports whether a file says it was written by a program.
//
// Editing one is worse than pointless: the change is erased the next time the
// generator runs, and the file says so. The standard library has 385 of them,
// and the loop rewrote one - index/suffixarray/sais2.go - stripping 512 lines
// of the comments that make a generated algorithm readable.
//
// The marker only counts before the package clause, which is where the
// convention puts it.
func generated(path string) bool {
	src, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(src), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "package ") {
			return false
		}
		if strings.HasPrefix(line, "// Code generated ") && strings.HasSuffix(line, " DO NOT EDIT.") {
			return true
		}
	}
	return false
}

// inCurrentBuild reports whether the build being gated actually compiles a
// file.
//
// A package directory holds more than one build. reflect has map_noswiss.go,
// math/big has arith_decl_pure.go, crypto/ecdsa has boring.go, and gob has a
// debug.go marked "//go:build ignore" that says in a comment it is not part of
// the package. None of them are compiled here, so `go build` and `go test`
// cannot say whether an edit to one is safe - and the dedup step writes to
// files directly, without gopls to refuse on its behalf. Editing one would
// break a configuration nothing in this loop ever compiles.
//
// go/build owns the question: it is the same constraint evaluation the
// toolchain does, for the same context, without shelling out.
func inCurrentBuild(path string) bool {
	ok, err := build.Default.MatchFile(filepath.Dir(path), filepath.Base(path))
	return err == nil && ok
}

// editableFilesIn is the files a change may be written to: the ones that ship,
// less the generated ones. Generated files are still read wherever the question
// is what refers to what, because a call from one keeps its target alive.
func editableFilesIn(dir string) ([]string, error) {
	files, err := goFilesIn(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, path := range files {
		if !generated(path) && inCurrentBuild(path) {
			out = append(out, path)
		}
	}
	return out, nil
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
