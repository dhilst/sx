package refactor

import (
	"bytes"
	"crypto/sha256"
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
)

// Cache is what detection keeps from one pass to the next.
//
// A pass used to start from nothing. Each detector parsed and type-checked
// the packages again, each package ran its own go list for export data, eg
// was launched once per template to type-check the whole program from
// source, and deadcode redid its whole-program analysis. On cc-connect, a
// 500k-node tree, that was 88 seconds and 4 GB a pass - and a pass comes
// before every attempt.
//
// Between two attempts almost nothing changes. So a package is read again
// only when its files, or the export data of a package it imports, differ
// from the last time, and what each detector found in it is kept with it.
type Cache struct {
	exports map[string]string // import path -> export data file, for this pass
	paths   map[string]string // package directory -> import path
	pkgs    map[string]*cachedPackage
	dead    []deadReport // deadcode's last answer
	deadOK  bool
}

type cachedPackage struct {
	key  [sha256.Size]byte
	tp   *typedPackage
	err  error // from type-checking, once tried
	memo map[string]any
}

// NewCache starts with nothing cached.
func NewCache() *Cache {
	return &Cache{pkgs: map[string]*cachedPackage{}}
}

// Begin starts a pass over the tree under dir. Export data is listed once for
// every package the tree depends on; a package that changed has a new export
// file, which is what marks its importers stale.
func (c *Cache) Begin(dir string) error {
	cmd := exec.Command("go", "list", "-e", "-deps", "-export", "-f", "{{.ImportPath}}={{.Export}}={{.Dir}}", "./...")
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go list: %w", err)
	}
	c.exports, c.paths = map[string]string{}, map[string]string{}
	for _, line := range strings.Split(out.String(), "\n") {
		parts := strings.SplitN(line, "=", 3)
		if len(parts) != 3 {
			continue
		}
		if parts[1] != "" {
			c.exports[parts[0]] = parts[1]
		}
		if parts[2] != "" {
			c.paths[parts[2]] = parts[0]
		}
	}
	return nil
}

// ForgetDead drops deadcode's answer, so the next pass asks again.
func (c *Cache) ForgetDead() { c.dead, c.deadOK = nil, false }

// buildFiles is what the current build compiles in dir, tests excluded.
func buildFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if path := filepath.Join(dir, name); inCurrentBuild(path) {
			out = append(out, path)
		}
	}
	return out
}

// packageDirs is every directory under dir holding a package the build
// compiles, in order.
func packageDirs(dir string) ([]string, error) {
	files, err := goFilesIn(dir)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, f := range files {
		if d := filepath.Dir(f); !seen[d] && inCurrentBuild(f) {
			seen[d] = true
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out, nil
}

// entry is dir's package as it stands now, parsed but not necessarily
// type-checked, reused when nothing it depends on has changed.
func (c *Cache) entry(dir string) (*cachedPackage, error) {
	if c.exports == nil {
		if err := c.Begin(dir); err != nil {
			return nil, err
		}
	}
	paths := buildFiles(dir)
	if len(paths) == 0 {
		return nil, fmt.Errorf("no Go files in %s", dir)
	}
	h := sha256.New()
	srcs := map[string][]byte{}
	imports := map[string]bool{}
	for _, path := range paths {
		src, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		srcs[path] = src
		sum := sha256.Sum256(src)
		h.Write([]byte(path))
		h.Write(sum[:])
		f, err := parser.ParseFile(token.NewFileSet(), path, src, parser.ImportsOnly)
		if err != nil {
			continue
		}
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			imports[p] = true
		}
	}
	var deps []string
	for p := range imports {
		deps = append(deps, p+"="+c.exports[p])
	}
	sort.Strings(deps)
	h.Write([]byte(strings.Join(deps, "\n")))
	var key [sha256.Size]byte
	copy(key[:], h.Sum(nil))
	if e, ok := c.pkgs[dir]; ok && e.key == key {
		return e, nil
	}
	tp := &typedPackage{fset: token.NewFileSet(), files: map[string]*ast.File{}}
	for _, path := range paths {
		f, err := parser.ParseFile(tp.fset, path, srcs[path], parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		tp.files[path] = f
	}
	e := &cachedPackage{key: key, tp: tp, memo: map[string]any{}}
	c.pkgs[dir] = e
	return e, nil
}

// syntax is dir's package parsed, without types.
func (c *Cache) syntax(dir string) (*typedPackage, error) {
	e, err := c.entry(dir)
	if err != nil {
		return nil, err
	}
	return e.tp, nil
}

// load is dir's package parsed and type-checked.
func (c *Cache) load(dir string) (*typedPackage, error) {
	e, err := c.entry(dir)
	if err != nil {
		return nil, err
	}
	if e.tp.info == nil && e.err == nil {
		e.err = e.tp.check(c.exports, c.paths[dir])
	}
	if e.err != nil {
		return nil, e.err
	}
	return e.tp, nil
}

// remember runs compute once per state of dir's package and of the extra
// files, which a detector reads besides the package (its tests, say).
func remember[T any](c *Cache, dir, kind string, extra []string, compute func() (T, error)) (T, error) {
	var zero T
	e, err := c.entry(dir)
	if err != nil {
		return zero, err
	}
	h := sha256.New()
	h.Write([]byte(kind))
	for _, path := range extra {
		src, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		sum := sha256.Sum256(src)
		h.Write([]byte(path))
		h.Write(sum[:])
	}
	k := string(h.Sum(nil))
	if v, ok := e.memo[k]; ok {
		return v.(T), nil
	}
	v, err := compute()
	if err != nil {
		return zero, err
	}
	e.memo[k] = v
	return v, nil
}

// identifiers is every identifier name in the package, for rejecting a
// pattern without type-checking anything.
func (c *Cache) identifiers(dir string) map[string]bool {
	ids, _ := remember(c, dir, "identifiers", nil, func() (map[string]bool, error) {
		tp, err := c.syntax(dir)
		if err != nil {
			return nil, err
		}
		out := map[string]bool{}
		for _, f := range tp.files {
			ast.Inspect(f, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok {
					out[id.Name] = true
				}
				return true
			})
		}
		return out, nil
	})
	return ids
}
