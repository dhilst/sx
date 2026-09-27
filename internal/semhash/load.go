// Package semhash proves that a change to a Go package kept its behaviour, by
// normalising the functions the change touched to a term graph whose identity
// is their meaning.
//
// Equal terms are a proof: a term's meaning depends only on the term itself
// (never on names, positions or types.Object pointers), and every
// normalisation step preserves meaning. So two functions whose terms are the
// same node behave the same. The converse cannot hold in general - program
// equivalence is undecidable - so unequal terms mean "not proved", never
// "different". The normaliser aims at the equivalences sx's own
// transformations produce: renaming, parameter passing, extracting and
// inlining helpers, early-return flags, dead code.
//
// Choices made while canonicalising (the order of guard atoms, of loop-carried
// variables, tie-breaks) only decide how much gets proved, never whether a
// proof is right.
//
// Assumptions a proof rests on: the program is free of data races; function
// names, PCs, runtime.Caller, stack depth, allocation counts and timing are
// not observed (extracting a function changes all of them); no package pulls
// an unexported function of this one through go:linkname; the build is the
// current GOOS/GOARCH/tags.
package semhash

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Snapshot is one version of a package, parsed and type-checked with every
// map of types.Info the proof needs.
type Snapshot struct {
	Dir        string
	ImportPath string
	GoVersion  string // "go1.25"
	Fset       *token.FileSet
	Files      []*ast.File // the package's build files, sorted by name; no tests
	Pkg        *types.Package
	Info       *types.Info
	Deps       []string // import paths the package depends on, transitively, sorted

	funcScope map[*types.Scope]string // a function's scope -> its key, for local type keys
}

type listed struct {
	ImportPath string
	Dir        string
	Export     string
	GoFiles    []string
	CgoFiles   []string
	Deps       []string
	Module     *struct{ GoVersion string }
	Error      *struct{ Err string }
}

func goList(dir string, args ...string) ([]listed, error) {
	cmd := exec.Command("go", append([]string{"list", "-e", "-json"}, args...)...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("go list: %v: %s", err, strings.TrimSpace(errb.String()))
	}
	var all []listed
	dec := json.NewDecoder(&out)
	for {
		var l listed
		if err := dec.Decode(&l); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		all = append(all, l)
	}
	return all, nil
}

// Load parses and type-checks the package in dir (its build files only).
func Load(dir string) (*Snapshot, error) {
	pkgs, err := goList(dir, ".")
	if err != nil {
		return nil, err
	}
	if len(pkgs) != 1 {
		return nil, fmt.Errorf("%s: expected one package, go list found %d", dir, len(pkgs))
	}
	p := pkgs[0]
	if p.Error != nil {
		return nil, fmt.Errorf("%s: %s", dir, p.Error.Err)
	}
	if len(p.CgoFiles) > 0 {
		return nil, fmt.Errorf("%s: cgo files are not modelled", dir)
	}
	deps, err := goList(dir, "-export", "-deps", ".")
	if err != nil {
		return nil, err
	}
	exports := map[string]string{}
	for _, d := range deps {
		exports[d.ImportPath] = d.Export
	}
	goVersion := ""
	if p.Module != nil && p.Module.GoVersion != "" {
		v := strings.Split(p.Module.GoVersion, ".")
		goVersion = "go" + strings.Join(v[:min(2, len(v))], ".")
	}
	fset := token.NewFileSet()
	var files []*ast.File
	names := append([]string(nil), p.GoFiles...)
	sort.Strings(names)
	for _, n := range names {
		f, err := parser.ParseFile(fset, filepath.Join(p.Dir, n), nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	imp := importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
		if f := exports[path]; f != "" {
			return os.Open(f)
		}
		return nil, fmt.Errorf("no export data for %s", path)
	})
	s, err := Check(fset, files, p.ImportPath, goVersion, imp)
	if err != nil {
		return nil, err
	}
	s.Dir = p.Dir
	s.Deps = append([]string(nil), p.Deps...)
	sort.Strings(s.Deps)
	return s, nil
}

// Check type-checks already parsed files as the package importPath.
func Check(fset *token.FileSet, files []*ast.File, importPath, goVersion string, imp types.Importer) (*Snapshot, error) {
	info := &types.Info{
		Types:        map[ast.Expr]types.TypeAndValue{},
		Defs:         map[*ast.Ident]types.Object{},
		Uses:         map[*ast.Ident]types.Object{},
		Implicits:    map[ast.Node]types.Object{},
		Selections:   map[*ast.SelectorExpr]*types.Selection{},
		Scopes:       map[ast.Node]*types.Scope{},
		Instances:    map[*ast.Ident]types.Instance{},
		FileVersions: map[*ast.File]string{},
	}
	var errs []string
	conf := types.Config{Importer: imp, GoVersion: goVersion, Error: func(err error) { errs = append(errs, err.Error()) }}
	pkg, _ := conf.Check(importPath, fset, files, info)
	if len(errs) > 0 {
		return nil, fmt.Errorf("type errors: %s", strings.Join(errs[:min(3, len(errs))], "; "))
	}
	s := &Snapshot{ImportPath: importPath, GoVersion: goVersion, Fset: fset, Files: files, Pkg: pkg, Info: info}
	s.funcScope = map[*types.Scope]string{}
	for _, f := range files {
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok {
				if sc := info.Scopes[fd.Type]; sc != nil {
					s.funcScope[sc] = s.funcKey(fd)
				}
			}
		}
	}
	return s, nil
}

// funcs is every function and method declared in the package, by key.
func (s *Snapshot) funcs() map[string]*ast.FuncDecl {
	out := map[string]*ast.FuncDecl{}
	seen := map[string]int{}
	for _, f := range s.Files {
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok {
				k := s.funcKey(fd)
				if fd.Name.Name == "init" || fd.Name.Name == "_" {
					// a package may declare many: number them in file order
					seen[k]++
					k += fmt.Sprintf("#%d", seen[k])
				}
				out[k] = fd
			}
		}
	}
	return out
}
