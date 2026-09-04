package refactor

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Apply performs a candidate's change and returns a function that undoes it.
//
// Every change is made on the text, using positions the parser recorded, and
// re-parsed before being written. A transformation that produces something Go
// cannot read is a bug in the transformation, not a smaller program.
func Apply(dir string, c Candidate, goplsPath string) (revert func() error, err error) {
	switch c.Kind {
	case KindDead:
		return removeDecl(c.File, c.Target)
	case KindInline:
		return inlineAndRemove(dir, goplsPath, c)
	case KindDuplicate:
		return extractDuplicate(dir, goplsPath, c)
	default:
		return nil, fmt.Errorf("no transformation for %q", c.Kind)
	}
}

// removeDecl deletes a function declaration and the doc comment that belongs
// to it. Leaving the comment behind would strand a description of something
// that is no longer there.
func removeDecl(path, name string) (func() error, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	var target *ast.FuncDecl
	for _, d := range file.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == shortName(name) {
			target = fn
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("no declaration of %s in %s", name, filepath.Base(path))
	}

	start := fset.Position(target.Pos()).Line
	if target.Doc != nil {
		start = fset.Position(target.Doc.Pos()).Line
	}
	end := fset.Position(target.End()).Line

	lines := strings.Split(string(src), "\n")
	if start < 1 || end > len(lines) {
		return nil, fmt.Errorf("%s spans lines outside the file", name)
	}
	kept := append([]string(nil), lines[:start-1]...)
	kept = append(kept, lines[end:]...)
	result := strings.Join(kept, "\n")

	if _, err := parser.ParseFile(token.NewFileSet(), path, result, parser.ParseComments); err != nil {
		return nil, fmt.Errorf("removing %s left the file unparseable: %w", name, err)
	}
	original := src
	if err := os.WriteFile(path, []byte(result), 0o644); err != nil {
		return nil, err
	}
	// Removing the last user of a package leaves its import behind, and Go
	// refuses to compile a file with an unused import. Both of the reverts in
	// the previous run were this, misread as the transformation being unsafe.
	tidyImports(path)
	return func() error { return os.WriteFile(path, original, 0o644) }, nil
}

// inlineAndRemove inlines a call and then deletes the function it called.
//
// Inlining on its own makes a program bigger, always: the body now exists at
// the call site and still exists in the declaration, so it is written twice.
// Measured on a nine-line example, 39 nodes became 43. The saving only appears
// when the abstraction goes with it - the same example reaches 22.
//
// Whether the declaration may go is not a question to answer by counting call
// sites, because a function can be referred to without being called. deadcode
// answers it properly, across the whole program, so the inline is kept only if
// it makes its target unreachable.
func inlineAndRemove(dir, goplsPath string, c Candidate) (func() error, error) {
	undoInline, err := inlineCall(dir, goplsPath, c)
	if err != nil {
		return nil, err
	}
	deadcodePath, ok := Tool("deadcode")
	if !ok {
		undoInline()
		return nil, fmt.Errorf("deadcode is needed to confirm the abstraction can go")
	}
	dead, err := DeadCandidates(deadcodePath, dir)
	if err != nil {
		undoInline()
		return nil, fmt.Errorf("could not confirm %s became unreachable: %w", c.Target, err)
	}
	var decl *Candidate
	for i := range dead {
		if shortName(dead[i].Target) == shortName(c.Target) {
			decl = &dead[i]
			break
		}
	}
	if decl == nil {
		undoInline()
		return nil, fmt.Errorf("%s is still reachable after inlining, so the body would just be written twice", c.Target)
	}
	undoRemove, err := removeDecl(decl.File, decl.Target)
	if err != nil {
		undoInline()
		return nil, err
	}
	return func() error {
		if err := undoRemove(); err != nil {
			return err
		}
		return undoInline()
	}, nil
}

// inlineCall asks gopls to replace a call with the body of what it calls.
// gopls owns this because substituting arguments for parameters safely needs
// types: it has to know that doing so changes neither the effects nor their
// order.
func inlineCall(dir, goplsPath string, c Candidate) (func() error, error) {
	if goplsPath == "" {
		return nil, fmt.Errorf("gopls is not installed")
	}
	original, err := os.ReadFile(c.File)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(dir, c.File)
	if err != nil {
		rel = c.File
	}
	spec := fmt.Sprintf("%s:%d:%d", rel, c.Line, c.Col)
	cmd := exec.Command(goplsPath, "codeaction", "-kind=refactor.inline.call", "-exec", "-w", spec)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("gopls declined to inline at %s: %s", spec, firstLine(stderr.String()))
	}
	return func() error { return os.WriteFile(c.File, original, 0o644) }, nil
}

func shortName(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i+1:]
	}
	return name
}

// extractDuplicate factors repeated code into one function.
//
// gopls extracts the first occurrence, because working out the free variables
// and the return values needs types. It extracts one range and knows nothing
// about the other copies, so the rest are replaced by hand with the call it
// generated - which is only sound because the copies are byte-identical and so
// name the same variables.
//
// That last step is a real hazard: the same identifiers in another function may
// refer to different things, and nothing here can tell. The tests are what
// stands between that and a wrong program.
func extractDuplicate(dir, goplsPath string, c Candidate) (func() error, error) {
	if goplsPath == "" {
		return nil, fmt.Errorf("gopls is not installed")
	}
	if len(c.Occurrences) < 2 {
		return nil, fmt.Errorf("a duplicate needs at least two occurrences")
	}
	originals, restore, err := snapshot(c.Occurrences)
	if err != nil {
		return nil, err
	}

	first := c.Occurrences[0]
	before, err := topLevelFuncs(first.File)
	if err != nil {
		restore()
		return nil, err
	}
	rel, err := filepath.Rel(dir, first.File)
	if err != nil {
		rel = first.File
	}
	spec := fmt.Sprintf("%s:%d:%d-%d:%d", rel, first.StartLine, first.StartCol, first.EndLine, first.EndCol)
	cmd := exec.Command(goplsPath, "codeaction", "-kind=refactor.extract.function", "-exec", "-w", spec)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		restore()
		return nil, fmt.Errorf("gopls declined to extract %s: %s", spec, firstLine(stderr.String()))
	}

	after, err := topLevelFuncs(first.File)
	if err != nil {
		restore()
		return nil, err
	}
	name := added(before, after)
	if name == "" {
		restore()
		return nil, fmt.Errorf("gopls extracted nothing at %s", spec)
	}
	call, err := callSite(first.File, name)
	if err != nil {
		restore()
		return nil, fmt.Errorf("could not find the call gopls generated: %w", err)
	}

	// The copies are re-found rather than trusted: extracting the first one
	// removed lines and appended a function, so every position recorded before
	// that has moved.
	// All the matches in a file are spliced in one pass, from the last to the
	// first. Going backwards keeps every earlier offset valid, so nothing has
	// to be re-found, and the file is formatted once at the end rather than
	// after each edit - reformatting between edits was what invalidated the
	// offsets and sent this into a 32-iteration loop that took four minutes.
	replaced := 0
	for _, path := range filesOf(c.Occurrences) {
		runs, err := FindRuns(path, c.Hash)
		if err != nil {
			restore()
			return nil, err
		}
		if len(runs) == 0 {
			continue
		}
		n, err := spliceAll(path, runs, call)
		if err != nil {
			restore()
			return nil, err
		}
		replaced += n
	}
	if replaced == 0 {
		restore()
		return nil, fmt.Errorf("the other copies of %s could not be found after extraction", c.Target)
	}
	_ = originals
	_ = originals
	return restore, nil
}

// snapshot remembers every file a change will touch.
func snapshot(occ []Occurrence) (map[string][]byte, func() error, error) {
	originals := map[string][]byte{}
	for _, o := range occ {
		if _, ok := originals[o.File]; ok {
			continue
		}
		b, err := os.ReadFile(o.File)
		if err != nil {
			return nil, nil, err
		}
		originals[o.File] = b
	}
	return originals, func() error {
		for path, b := range originals {
			if err := os.WriteFile(path, b, 0o644); err != nil {
				return err
			}
		}
		return nil
	}, nil
}

func topLevelFuncs(path string) (map[string]bool, error) {
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok {
			out[fn.Name.Name] = true
		}
	}
	return out, nil
}

func added(before, after map[string]bool) string {
	for name := range after {
		if !before[name] {
			return name
		}
	}
	return ""
}

// callSite prints the statement that calls the extracted function, which is
// what every other copy is replaced by.
func callSite(path, name string) (string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return "", err
	}
	var found ast.Stmt
	ast.Inspect(f, func(n ast.Node) bool {
		if found != nil {
			return false
		}
		stmt, ok := n.(ast.Stmt)
		if !ok {
			return true
		}
		calls := false
		ast.Inspect(stmt, func(x ast.Node) bool {
			if call, ok := x.(*ast.CallExpr); ok {
				if id, ok := call.Fun.(*ast.Ident); ok && id.Name == name {
					calls = true
				}
			}
			return true
		})
		if calls {
			if _, isBlock := stmt.(*ast.BlockStmt); !isBlock {
				found = stmt
			}
		}
		return true
	})
	if found == nil {
		return "", fmt.Errorf("no call to %s", name)
	}
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, found); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// spliceAll replaces every occurrence in one file, last first.
func spliceAll(path string, runs []Occurrence, call string) (int, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].StartOffset > runs[j].StartOffset })
	out := string(src)
	replaced, lastStart := 0, len(out)+1
	for _, o := range runs {
		if o.EndOffset > lastStart {
			continue // overlaps one already spliced
		}
		if o.StartOffset < 0 || o.EndOffset > len(out) || o.StartOffset >= o.EndOffset {
			return 0, fmt.Errorf("%s:%d spans bytes outside the file", filepath.Base(path), o.StartLine)
		}
		indent := indentOf(out, o.StartOffset)
		body := strings.ReplaceAll(call, "\n", "\n"+indent)
		out = out[:o.StartOffset] + body + out[o.EndOffset:]
		lastStart = o.StartOffset
		replaced++
	}
	formatted, err := format.Source([]byte(out))
	if err != nil {
		return 0, fmt.Errorf("replacing copies in %s left the file unparseable: %w", filepath.Base(path), firstErr(err))
	}
	if err := os.WriteFile(path, formatted, 0o644); err != nil {
		return 0, err
	}
	return replaced, nil
}

// indentOf is the whitespace at the start of the line the offset sits on.
func indentOf(src string, offset int) string {
	start := strings.LastIndexByte(src[:offset], '\n') + 1
	line := src[start:offset]
	return line[:len(line)-len(strings.TrimLeft(line, " \t"))]
}

func filesOf(occ []Occurrence) []string {
	seen := map[string]bool{}
	var out []string
	for _, o := range occ {
		if !seen[o.File] {
			seen[o.File] = true
			out = append(out, o.File)
		}
	}
	sort.Strings(out)
	return out
}

func firstErr(err error) error {
	if s := firstLine(err.Error()); s != "" {
		return fmt.Errorf("%s", s)
	}
	return err
}
