package refactor

import (
	"bytes"
	"errors"
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
func Apply(dir string, c Candidate, goplsPath, egPath string) (revert func() error, err error) {
	switch c.Kind {
	case KindDead:
		return removeDecl(c.File, c.Target)
	case KindInline:
		undoInline, err := func() (func() error, error) {
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
			undo := func() error { return os.WriteFile(c.File, original, 0o644) }
			if err := parses(c.File); err != nil {
				undo()
				return nil, fmt.Errorf("gopls's inline left %s unparseable: %w", filepath.Base(c.File), firstErr(err))
			}
			return undo, nil
		}()
		if err != nil {
			return nil, err
		}
		// The package the call sits in, not the whole tree: an unexported
		// function belongs to its package, so that is the scope in which
		// "nothing refers to it any more" is the right question.
		declFile, err := func() (string, error) {
			var pkgDir, name string = filepath.Dir(c.File), shortName(c.Target)
			if deadcodePath, ok := Tool("deadcode"); ok {
				if dead, err := DeadCandidates(deadcodePath, dir); err == nil {
					for _, d := range dead {
						if shortName(d.Target) == name && filepath.Dir(d.File) == pkgDir {
							return d.File, nil
						}
					}
					return "", fmt.Errorf("%s is still reachable after inlining, so the body would just be written twice", name)
				}
			}
			path, ok := func() (string, bool) {
				files, err := editableFilesIn(pkgDir)
				if err != nil {
					return "", false
				}
				for _, path := range files {
					f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
					if err != nil {
						continue
					}
					for _, d := range f.Decls {
						if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == name {
							return path, true
						}
					}
				}
				return "", false
			}()
			if !ok {
				return "", fmt.Errorf("no declaration of %s in %s", name, filepath.Base(pkgDir))
			}
			if ast.IsExported(name) {
				return "", fmt.Errorf("%s is exported, so what refers to it cannot be known from here", name)
			}
			if func() bool {
				found := false
				filepath.WalkDir(pkgDir, func(path string, d os.DirEntry, err error) error {
					if err != nil || found {
						return nil
					}
					if d.IsDir() {
						if path != pkgDir && skipDir(d.Name()) {
							return filepath.SkipDir
						}
						return nil
					}
					switch {
					case strings.HasSuffix(path, ".s"):
						b, err := os.ReadFile(path)
						if err == nil && bytes.Contains(b, []byte(name)) {
							found = true
						}
					case strings.HasSuffix(path, ".go"):
						b, err := os.ReadFile(path)
						if err != nil {
							return nil
						}
						for _, line := range strings.Split(string(b), "\n") {
							if strings.Contains(line, "//go:linkname") && strings.Contains(line, name) {
								found = true
								return nil
							}
						}
					}
					return nil
				})
				return found
			}() {
				return "", fmt.Errorf("%s is named by assembly or //go:linkname, which a parser cannot follow", name)
			}
			n, err := func() (int, error) {
				files, err := goFilesIn(pkgDir)
				if err != nil {
					return 0, err
				}
				tests, err := testFilesIn(pkgDir)
				if err != nil {
					return 0, err
				}
				total := 0
				for _, path := range append(files, tests...) {
					f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
					if err != nil {
						return 0, err
					}
					ast.Inspect(f, func(n ast.Node) bool {
						if id, ok := n.(*ast.Ident); ok && id.Name == name {
							total++
						}
						return true
					})
				}
				return total, nil
			}()
			if err != nil {
				return "", err
			}
			if n > 1 {
				return "", fmt.Errorf("%s still has %d references after inlining, so the body would just be written twice", name, n-1)
			}
			return path, nil
		}()
		if err != nil {
			undoInline()
			return nil, err
		}
		undoRemove, err := removeDecl(declFile, shortName(c.Target))
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
	case KindDuplicate:
		if goplsPath == "" {
			return nil, fmt.Errorf("gopls is not installed")
		}
		if len(c.Occurrences) < 2 {
			return nil, fmt.Errorf("a duplicate needs at least two occurrences")
		}
		originals, restore, err := func() (map[string][]byte, func() error, error) {
			originals := map[string][]byte{}
			for _, o := range c.Occurrences {
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
		}()
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
		// gopls's own output is checked before anything is built on top of it.
		// An extraction in math/big left rat.go unparseable, and the first
		// thing to notice was a parse error from a later step reported against
		// a column that does not exist in the file anyone would look at.
		if err := parses(first.File); err != nil {
			restore()
			return nil, fmt.Errorf("gopls's extraction left %s unparseable: %w", filepath.Base(first.File), firstErr(err))
		}
		after, err := topLevelFuncs(first.File)
		if err != nil {
			restore()
			return nil, err
		}
		name := func() string {
			for name := range after {
				if !before[name] {
					return name
				}
			}
			return ""
		}()
		if name == "" {
			restore()
			return nil, fmt.Errorf("gopls extracted nothing at %s", spec)
		}
		call, bare, err := callSite(first.File, name)
		if err != nil {
			restore()
			return nil, fmt.Errorf("could not find the call gopls generated: %w", err)
		}
		if !bare {
			restore()
			return nil, fmt.Errorf("gopls extracted %s as returning values, so the copies cannot be replaced by a call", name)
		}
		replaced := 0
		for _, path := range func() []string {
			seen := map[string]bool{}
			var out []string
			for _, o := range c.Occurrences {
				if !seen[o.File] {
					seen[o.File] = true
					out = append(out, o.File)
				}
			}
			sort.Strings(out)
			return out
		}() {
			runs, err := FindRuns(path, c.Hash)
			if err != nil {
				restore()
				return nil, err
			}
			if path == first.File {
				start, end, err := declSpan(path, name)
				if err != nil {
					restore()
					return nil, err
				}
				runs = outside(runs, start, end)
			}
			if len(runs) == 0 {
				continue
			}
			n, err := func() (int, error) {
				var runs []Occurrence = runs
				src, err := os.ReadFile(path)
				if err != nil {
					return 0, err
				}
				sort.SliceStable(runs, func(i, j int) bool {
					return runs[i].StartOffset > runs[j].StartOffset
				})
				out := string(src)
				replaced, lastStart := 0, len(out)+1
				for _, o := range runs {
					if o.EndOffset > lastStart {
						continue
					}
					if o.StartOffset < 0 || o.EndOffset > len(out) || o.StartOffset >= o.EndOffset {
						return 0, fmt.Errorf("%s:%d spans bytes outside the file", filepath.Base(path), o.StartLine)
					}
					indent := func() string {
						start := strings.LastIndexByte(out[:o.StartOffset], '\n') + 1
						line := out[start:o.StartOffset]
						return line[:len(line)-len(strings.TrimLeft(line, " \t"))]
					}()
					body := strings.ReplaceAll(call, "\n", "\n"+indent)
					out = out[:o.StartOffset] + body + out[o.EndOffset:]
					lastStart = o.StartOffset
					replaced++
				}
				formatted, err := format.Source([]byte(out))
				if err != nil {
					return 0, fmt.Errorf("replacing copies in %s left the file unparseable: %w", filepath.Base(path), func() error {
						if s := firstLine(err.Error()); s != "" {
							return errors.New(s)
						}
						return err
					}())
				}
				if err := os.WriteFile(path, formatted, 0o644); err != nil {
					return 0, err
				}
				return replaced, nil
			}()
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
	case KindEg:
		if egPath == "" {
			return nil, fmt.Errorf("eg is not installed")
		}
		if c.Template == "" {
			return nil, fmt.Errorf("eg candidate has no template")
		}
		originals, restore, err := func() (map[string][]byte, func() error, error) {
			files, err := goFilesIn(dir)
			if err != nil {
				return nil, nil, err
			}
			tests, err := testFilesIn(dir)
			if err != nil {
				return nil, nil, err
			}
			originals := map[string][]byte{}
			for _, path := range append(files, tests...) {
				b, err := os.ReadFile(path)
				if err != nil {
					return nil, nil, err
				}
				originals[path] = b
			}
			return originals, func() error {
				for path, b := range originals {
					if err := os.WriteFile(path, b, 0o644); err != nil {
						return err
					}
				}
				return nil
			}, nil
		}()
		if err != nil {
			return nil, err
		}
		cmd := exec.Command(egPath, "-w", "-t", c.Template, "./...")
		cmd.Dir = dir
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			restore()
			return nil, fmt.Errorf("eg declined %s: %s", filepath.Base(c.Template), firstLine(stderr.String()))
		}
		var changed []string
		for path, before := range originals {
			after, err := os.ReadFile(path)
			if err != nil {
				restore()
				return nil, err
			}
			if !bytes.Equal(before, after) {
				changed = append(changed, path)
			}
		}
		if len(changed) == 0 {
			restore()
			return nil, fmt.Errorf("eg found no matches for %s", filepath.Base(c.Template))
		}
		for _, path := range changed {
			if generated(path) {
				restore()
				return nil, fmt.Errorf("eg rewrote generated file %s", filepath.Base(path))
			}
			if !inCurrentBuild(path) {
				restore()
				return nil, fmt.Errorf("eg rewrote file outside this build: %s", filepath.Base(path))
			}
			if err := parses(path); err != nil {
				restore()
				return nil, fmt.Errorf("eg left %s unparseable: %w", filepath.Base(path), firstErr(err))
			}
		}
		return restore, nil
	default:
		return nil, fmt.Errorf("no transformation for %q", c.Kind)
	}
}

// removeDecl deletes a function declaration and the doc comment that belongs
// to it. Leaving the comment behind would strand a description of something
// that is no longer there.
func removeDecl(path, name string) (func() error, error) {
	if generated(path) {
		return nil, fmt.Errorf("%s is generated, so an edit to it would be undone", filepath.Base(path))
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	// Only a plain function. Go allows a method to share a name with one -
	// func foo() alongside func (t *T) foo() - and matching on the name alone
	// deleted whichever came first in the file. declaringFile has always
	// checked the receiver; the function that does the deleting did not.
	var target *ast.FuncDecl
	for _, d := range file.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == shortName(name) {
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

func shortName(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i+1:]
	}
	return name
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

// parses reports whether a file is still readable Go.
func parses(path string) error {
	_, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	return err
}

// declSpan is the byte range a top-level function occupies.
func declSpan(path, name string) (int, int, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return 0, 0, err
	}
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == name {
			return fset.Position(fn.Pos()).Offset, fset.Position(fn.End()).Offset, nil
		}
	}
	return 0, 0, fmt.Errorf("no declaration of %s in %s", name, filepath.Base(path))
}

// outside drops the occurrences that lie within a byte range.
func outside(runs []Occurrence, start, end int) []Occurrence {
	var out []Occurrence
	for _, o := range runs {
		if o.StartOffset >= start && o.EndOffset <= end {
			continue
		}
		out = append(out, o)
	}
	return out
}

// callSite prints the statement that calls the extracted function, which is
// what every other copy is replaced by. The second result reports whether that
// statement is a bare call rather than one that takes values out of it.
func callSite(path, name string) (string, bool, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return "", false, err
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
		return "", false, fmt.Errorf("no call to %s", name)
	}
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, found); err != nil {
		return "", false, err
	}
	_, bare := found.(*ast.ExprStmt)
	return buf.String(), bare, nil
}

// firstErr reduces a parser's error list to the first line, which is the one
// that says what actually went wrong.
func firstErr(err error) error {
	if s := firstLine(err.Error()); s != "" {
		return errors.New(s)
	}
	return err
}
