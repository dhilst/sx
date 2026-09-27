package refactor

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/dhilst/sx/internal/cost"
)

// Extracting a repeated run is priced by building, node for node, what gopls's
// extract-function produces and counting it:
//
//	ΔN = F + D·C − (D−1)·B
//
// B is the run, D the number of copies, F what the new declaration adds
// besides the body it takes over, and C what replaces each copy. A flat
// guess at F and C predicted savings for every run that defines a variable
// used afterwards, and each one cost a gopls round trip to find out that the
// call gopls wrote needed a result list, parameter types, and sometimes a
// return-flag check that together outweighed the copies removed.
//
// The rules below follow gopls v0.23 (internal/golang/extract.go). Where
// they disagree with it, the measurement after the change still decides;
// test/examples records the cases the two have been checked against.

// prediction is a model's account of one change.
type prediction interface {
	Delta() int // the predicted change in |AST|
	String() string
}

// Extraction is the model's account of one extraction.
type Extraction struct {
	B, D, F, C int
	Params     []string // free variables that become parameters
	Results    []string // values the caller needs back
	Control    string   // how returns inside the run reach the caller

	// The statements the call site takes, all at the run's depth but Deep of
	// them one deeper ("if err != nil { return ... }"), and those the new
	// function adds to the run: the return appended to its body.
	Site, Deep, Added int
}

// Delta is the change in |AST|: negative when the extraction shrinks the tree.
func (e Extraction) Delta() int { return e.F + e.D*e.C - (e.D-1)*e.B }

func (e Extraction) String() string {
	var why []string
	if len(e.Params) > 0 {
		why = append(why, "params "+strings.Join(e.Params, ", "))
	}
	if len(e.Results) > 0 {
		why = append(why, "results "+strings.Join(e.Results, ", "))
	}
	if e.Control != "" {
		why = append(why, e.Control)
	}
	s := fmt.Sprintf("B=%d D=%d F=%d C=%d removed=%d ΔN=%+d", e.B, e.D, e.F, e.C, (e.D-1)*e.B, e.Delta())
	if len(why) > 0 {
		s += " (" + strings.Join(why, "; ") + ")"
	}
	return s
}

// typedPackage is one directory's package, parsed with a shared FileSet and
// type-checked, which is what the model needs to know a variable's type.
type typedPackage struct {
	fset  *token.FileSet
	files map[string]*ast.File
	pkg   *types.Package
	info  *types.Info
	uses  map[types.Object][]*ast.Ident // info.Uses by object, built on first need
}

// usesOf is every identifier that refers to obj. The extraction model asked
// this for every variable of every candidate run by scanning info.Uses, which
// was a quarter of a detection pass on a large package.
func (tp *typedPackage) usesOf(obj types.Object) []*ast.Ident {
	if tp.uses == nil {
		tp.uses = map[types.Object][]*ast.Ident{}
		for id, o := range tp.info.Uses {
			tp.uses[o] = append(tp.uses[o], id)
		}
	}
	return tp.uses[obj]
}

// check type-checks the package. Imports are read from export data, the
// same thing the compiler reads, listed once per pass by Cache.Begin.
func (tp *typedPackage) check(exports map[string]string, path string) (err error) {
	// The importer panics on export data newer than the Go that built sx:
	// flowstate needs Go 1.27, and sx built with 1.25 cannot read what its
	// toolchain writes. A package that cannot be type-checked is left out of
	// the models, not allowed to end the run.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("type-checking: %v", r)
		}
	}()
	conf := types.Config{
		Importer: importer.ForCompiler(tp.fset, "gc", func(path string) (io.ReadCloser, error) {
			file, ok := exports[path]
			if !ok {
				return nil, fmt.Errorf("no export data for %s", path)
			}
			return os.Open(file)
		}),
		Error: func(error) {}, // a partial answer is still an answer
	}
	tp.info = &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Scopes:     map[ast.Node]*types.Scope{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
	}
	var files []*ast.File
	var paths []string
	for path := range tp.files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		files = append(files, tp.files[path])
	}
	if len(files) == 0 {
		return fmt.Errorf("no files")
	}
	// Checked under its import path, so its objects are the ones an
	// importer sees; a bare package name made m/a's New a different
	// function from the a.New that m/b calls.
	if path == "" {
		path = files[0].Name.Name
	}
	pkg, err := conf.Check(path, tp.fset, files, tp.info)
	if pkg == nil {
		return err
	}
	tp.pkg = pkg
	return nil
}

// predictExtraction prices extracting stmts, which sit in file, when d copies
// of them would be replaced by the call. copies are the files the other
// copies sit in: a declaration gopls writes at the call site has to compile
// in each of them.
func predictExtraction(tp *typedPackage, file *ast.File, stmts []ast.Stmt, d int, copies []*ast.File) (Extraction, error) {
	return predictExtractionTail(tp, file, stmts, d, copies, false)
}

// predictExtractionTail is predictExtraction that, when tail is set, lets a
// run that ends by returning from the enclosing function take its defers
// along. The call is then "return f(...)": nothing of the caller runs between
// the helper returning and the caller returning, so each deferred call still
// runs as the caller returns, in the same order, on the normal path and on a
// panic. Two things still tie a defer to the caller's frame: recover, which
// only stops a panic in the frame it was deferred in, and named results,
// which a deferred call can change after the return has set them.
func predictExtractionTail(tp *typedPackage, file *ast.File, stmts []ast.Stmt, d int, copies []*ast.File, tail bool) (Extraction, error) {
	info := tp.info
	start, end := stmts[0].Pos(), stmts[len(stmts)-1].End()
	e := Extraction{D: d}
	for _, s := range stmts {
		e.B += cost.Count(s)
	}

	// The enclosing function, and every node's parent within it.
	var outer *ast.FuncDecl
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil && fn.Pos() <= start && end <= fn.End() {
			outer = fn
		}
	}
	if outer == nil {
		return e, fmt.Errorf("no enclosing function")
	}
	parent := map[ast.Node]ast.Node{}
	var stack []ast.Node
	ast.Inspect(outer, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		if len(stack) > 0 {
			parent[n] = stack[len(stack)-1]
		}
		stack = append(stack, n)
		return true
	})
	block := parent[stmts[0]] // a BlockStmt or a CaseClause: what encloses the run

	// Imports, by path, so a type the new code names can be checked against
	// what each file already imports. gopls names the package but does not
	// add the import.
	imported := func(f *ast.File) map[string]string {
		m := map[string]string{}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			name := ""
			if imp.Name != nil {
				name = imp.Name.Name
			}
			m[path] = name
		}
		return m
	}
	var missing []string
	qualifier := func(f *ast.File) types.Qualifier {
		imports := imported(f)
		return func(p *types.Package) string {
			if p == nil || p == tp.pkg {
				return ""
			}
			name, ok := imports[p.Path()]
			if !ok {
				missing = append(missing, p.Path())
			}
			switch name {
			case ".":
				return ""
			case "", "_":
				return p.Name()
			}
			return name
		}
	}
	qual := qualifier(file)
	typeCost := func(t types.Type, q types.Qualifier) int {
		expr, err := parser.ParseExpr(types.TypeString(t, q))
		if err != nil {
			return 1
		}
		return cost.Count(expr)
	}
	var zeroCost func(t types.Type) int
	zeroCost = func(t types.Type) int {
		switch t := t.(type) {
		case *types.Named, *types.Alias:
			switch t.Underlying().(type) {
			case *types.Struct, *types.Array:
				return 1 + typeCost(t, qual) // T{}
			}
			return zeroCost(t.Underlying())
		case *types.Array, *types.Struct:
			return 1 + typeCost(t, qual)
		case *types.TypeParam:
			return 4 // *new(T)
		}
		return 1 // 0, "", false or nil
	}

	// Returns inside the run. One at the top level of the run always runs, so
	// the caller can return the call's result directly; nested ones need the
	// caller to learn whether to return, from a flag or, when every one of
	// them is "if err != nil { return ..., err }", from the error itself.
	errorType := types.Universe.Lookup("error").Type()
	var returns []*ast.ReturnStmt
	hasNonNested, allErr := false, true
	ast.Inspect(block, func(n ast.Node) bool {
		if lit, ok := n.(*ast.FuncLit); ok {
			return !(start < lit.Pos() && lit.End() < end)
		}
		ret, ok := n.(*ast.ReturnStmt)
		if !ok {
			return true
		}
		if ret.Pos() < start || ret.End() > end {
			return false
		}
		returns = append(returns, ret)
		if parent[ret] == block {
			hasNonNested = true
		}
		if allErr && !errReturn(info, parent, ret, errorType) {
			allErr = false
		}
		return false
	})
	hasReturn := len(returns) > 0
	if what := frameBound(info, stmts); what != "" && !(tail && what == "a defer" && tailReturns(info, parent, outer, block, stmts)) {
		// A defer runs when the function it is in returns, and recover
		// only works in a deferred call of the panicking frame. Moved into
		// the helper, "configMu.Lock(); defer configMu.Unlock()" released
		// cc-connect's config lock as soon as the helper returned, and the
		// caller read, changed and saved the file unlocked; "defer
		// resp.Body.Close()" closed a body the helper then returned. Both
		// compiled, passed every test, and shrank the tree.
		return e, fmt.Errorf("the run contains %s, which belongs to the enclosing function's frame", what)
	}
	for _, st := range stmts {
		var named string
		ast.Inspect(st, func(n ast.Node) bool {
			if ts, ok := n.(*ast.TypeSpec); ok && named == "" {
				named = ts.Name.Name
			}
			return named == ""
		})
		if named != "" {
			// A type declared in the run goes with it, out of the scope of
			// everything after the run that names it: extracting
			// Cache.inlinesIn's "type decl struct" left the rest of the
			// function naming a type that was no longer there.
			return e, fmt.Errorf("the run declares type %s", named)
		}
	}
	if name := fieldWrite(info, stmts); name != "" {
		// gopls passes the variable in by value and does not hand it back,
		// so the write lands on the helper's copy. Extracting
		// "m.S = ...; m.R = ..." out of predictInline made every inline
		// predict a change of zero, and only the model tests noticed.
		return e, fmt.Errorf("the run writes a field or element of %s, which the new function would get a copy of", name)
	}
	if freeBranch(info, parent, stmts, start, end) {
		// gopls threads these through a control value and a switch at the
		// call site. That is not modelled, so it is not attempted.
		return e, fmt.Errorf("a break, continue or goto leaves the run")
	}
	errCase := hasReturn && allErr

	// Every variable the run mentions: which become parameters, which come
	// back as results, and whether the call can use :=.
	vars, err := runVariables(info, file, block, start, end)
	if err != nil {
		return e, err
	}
	type field struct{ name, typ int }
	var signature []types.Type // every type the new function's signature names
	var params []field
	var resultTypes []types.Type
	var nResults, uninitialized, canRedefine int
	seenUninit := map[types.Object]bool{}
	var declTypes []types.Type // what "var x T" before the call would declare
	for _, v := range vars {
		if v.obj.Name() == "_" || v.obj.Parent() == nil {
			continue
		}
		used, firstUse := usedIn(tp, end, v.obj.Parent().End(), v.obj)
		result := v.assigned && used && !overridden(info, firstUse, v.obj, v.free, outer)
		param := v.free && !v.defined
		if (result || param) && aliased(info, stmts, v.obj, written(info, outer, v.obj)) {
			// gopls passes and returns by value. Whatever holds the address
			// would keep the helper's copy: "var buf bytes.Buffer;
			// cmd.Stderr = &buf" was extracted exactly so, and the build,
			// the tests and the measure all passed it.
			return e, fmt.Errorf("%s would be copied after its address is taken", v.obj.Name())
		}
		if v.free && v.assigned && !result && readLater(info, outer, parent, stmts, v.obj) {
			// Written in the run but not handed back, because nothing
			// after it in the source reads it - yet a loop around the run
			// or a closure does, and would see the old value.
			return e, fmt.Errorf("the write to %s would be lost", v.obj.Name())
		}
		if result {
			nResults++
			resultTypes = append(resultTypes, v.obj.Type())
			signature = append(signature, v.obj.Type())
			e.Results = append(e.Results, v.obj.Name())
			if !v.free {
				if !seenUninit[v.obj] {
					seenUninit[v.obj] = true
					declTypes = append(declTypes, v.obj.Type())
				}
				uninitialized++
			} else {
				scope := v.obj.Parent()
				if scope.Pos() == block.Pos() || block == outer.Body && scope == info.Scopes[outer.Type] {
					canRedefine++
				}
			}
		}
		if param {
			for _, name := range e.Params {
				if name == v.obj.Name() {
					// A type switch declares its variable once per clause;
					// gopls would name two parameters the same.
					return e, fmt.Errorf("two variables named %s would become parameters", name)
				}
			}
			params = append(params, field{1, typeCost(v.obj.Type(), qual)})
			signature = append(signature, v.obj.Type())
			e.Params = append(e.Params, v.obj.Name())
		}
	}

	// The enclosing function's results, when a return has to be carried out.
	type retVar struct{ decl, zero int }
	var retVars []retVar
	ifCost := 0
	if hasReturn {
		sig := outer.Type
		for n := parent[block]; n != nil && n != outer; n = parent[n] {
			if lit, ok := n.(*ast.FuncLit); ok {
				sig = lit.Type
				break
			}
		}
		if sig.Results != nil {
			for _, f := range sig.Results.List {
				t := info.TypeOf(f.Type)
				if t == nil {
					return e, fmt.Errorf("no type for a result of the enclosing function")
				}
				for range max(1, len(f.Names)) {
					retVars = append(retVars, retVar{typeCost(t, qual), zeroCost(t)})
					signature = append(signature, t)
					declTypes = append(declTypes, t)
				}
			}
		}
		names := len(retVars)
		if !hasNonNested {
			if name := returnedOuter(info, returns, start, end); name != "" {
				// gopls names the values it carries back after what the
				// returns return, and declares them with := at the call,
				// which reuses a variable already there. The path that does
				// not return then hands back a zero value into it: extracting
				// part of predictExtraction wrote "..., e, err, shouldReturn
				// := newFunction2(...)" and every model priced nothing.
				return e, fmt.Errorf("a return inside the run returns %s, which the call would overwrite", name)
			}
		}
		switch {
		case hasNonNested:
			e.Control = "trailing return: the call is returned"
		case errCase:
			e.Control = "error returns: checked with err != nil"
			if names > 0 {
				ifCost = 6 + names // if X != nil { return ... }
			}
		default:
			e.Control = "nested returns: a shouldReturn flag"
			retVars = append(retVars, retVar{1, 1})
			declTypes = append(declTypes, types.Typ[types.Bool])
			ifCost = 4 + names // if flag { return ... }
		}
		if !hasNonNested {
			// Every return in the body also hands back zero values for the
			// results, and the flag.
			extra := 0
			for _, t := range resultTypes {
				extra += zeroCost(t)
			}
			if !errCase {
				extra++
			}
			e.F += len(returns) * extra
		}
	}

	hasValues := nResults+len(retVars) > 0
	if hasValues && !hasNonNested {
		e.F += 1 + nResults // the return appended to the new body
		for _, r := range retVars {
			e.F += r.zero
		}
	}

	// The declaration: func name(p T, ...) (R, ...) { body }
	e.F += 4 // FuncDecl, its name, FuncType, parameter list
	for _, p := range params {
		e.F += 1 + p.name + p.typ
	}
	if nr := len(resultTypes) + len(retVars); nr > 0 {
		e.F++
		for _, t := range resultTypes {
			e.F += 1 + typeCost(t, qual)
		}
		for _, r := range retVars {
			e.F += 1 + r.decl
		}
	}
	e.F++ // the body block

	// The call site, written once and copied to every other occurrence.
	call := 2 + len(params)
	canDefineCount := uninitialized + canRedefine
	switch {
	case !hasValues:
		e.C = 1 + call // name(args)
	case hasNonNested:
		e.C = 1 + call // return name(args)
	default:
		e.C = 1 + nResults + len(retVars) + call // a, b := name(args)
	}
	if canDefineCount != nResults {
		// var a T before the call, in every file a copy sits in.
		for _, t := range declTypes {
			n := 0
			for _, f := range copies {
				n = typeCost(t, qualifier(f))
			}
			e.C += 4 + n
		}
	}
	e.C += ifCost
	e.Site = 1
	if canDefineCount != nResults {
		e.Site += len(declTypes)
	}
	if ifCost > 0 {
		e.Site, e.Deep = e.Site+2, 1
	}
	if hasValues && !hasNonNested {
		e.Added = 1
	}
	if len(missing) > 0 {
		return e, fmt.Errorf("gopls would name %s without importing it", missing[0])
	}
	for _, t := range signature {
		if generic(t) {
			// gopls does not carry the enclosing function's type
			// parameters over, so the new function names a T it does not
			// declare.
			return e, fmt.Errorf("the signature would need type parameter %s", t)
		}
	}
	return e, nil
}

// errReturn reports whether ret is "return ..., err" as the whole body of an
// "if err != nil" - the shape gopls lifts into the caller as an error check.
func errReturn(info *types.Info, parent map[ast.Node]ast.Node, ret *ast.ReturnStmt, errorType types.Type) bool {
	if len(ret.Results) == 0 {
		return false
	}
	if t := info.TypeOf(ret.Results[len(ret.Results)-1]); t == nil || !types.Identical(t, errorType) {
		return false
	}
	body, ok := parent[ret].(*ast.BlockStmt)
	if !ok || len(body.List) > 1 {
		return false
	}
	ifs, ok := parent[body].(*ast.IfStmt)
	if !ok {
		return false
	}
	cond, ok := ifs.Cond.(*ast.BinaryExpr)
	if !ok || cond.Op != token.NEQ {
		return false
	}
	tx, ty := info.TypeOf(cond.X), info.TypeOf(cond.Y)
	return tx != nil && types.Identical(tx, errorType) && ty != nil && types.Identical(ty, types.Typ[types.UntypedNil])
}

// freeBranch reports whether a break, continue or goto in the run jumps to
// something outside it.
func freeBranch(info *types.Info, parent map[ast.Node]ast.Node, stmts []ast.Stmt, start, end token.Pos) bool {
	free := false
	for _, s := range stmts {
		ast.Inspect(s, func(n ast.Node) bool {
			br, ok := n.(*ast.BranchStmt)
			if !ok || free {
				return !free
			}
			label, _ := info.Uses[br.Label].(*types.Label)
			if label != nil && !(start <= label.Pos() && label.Pos() <= end) {
				free = true
				return false
			}
			if br.Tok != token.BREAK && br.Tok != token.CONTINUE {
				return false
			}
			for a := parent[br]; a != nil; a = parent[a] {
				if l, ok := parent[a].(*ast.LabeledStmt); ok && label != nil && l.Label.Name == label.Name() {
					continue
				}
				switch a.(type) {
				case *ast.ForStmt, *ast.RangeStmt:
					free = a.Pos() < start
					return false
				case *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
					if br.Tok == token.BREAK {
						free = a.Pos() < start
						return false
					}
				}
			}
			return false
		})
	}
	return free
}

type runVariable struct {
	obj      types.Object
	free     bool // declared before the run
	assigned bool // written inside the run
	defined  bool // first mention inside the run is its own declaration
}

// runVariables lists the local variables the run mentions, in order of first
// mention, the way gopls's collectFreeVars does.
func runVariables(info *types.Info, file *ast.File, block ast.Node, start, end token.Pos) ([]*runVariable, error) {
	fileScope := info.Scopes[file]
	if fileScope == nil {
		return nil, fmt.Errorf("no scope for %s", file.Name.Name)
	}
	id := func(n *ast.Ident) (types.Object, bool) {
		obj := info.Uses[n]
		if obj == nil {
			return info.Defs[n], false
		}
		if obj.Name() == "_" {
			return nil, false
		}
		if _, ok := obj.(*types.PkgName); ok {
			return nil, false
		}
		if !(file.FileStart <= obj.Pos() && obj.Pos() <= file.FileEnd) {
			return nil, false
		}
		scope := obj.Parent()
		if scope == nil || scope == fileScope || scope == fileScope.Parent() {
			return nil, false // package or file scope
		}
		if start <= obj.Pos() && obj.Pos() <= end {
			return obj, false
		}
		return obj, true
	}
	var sel func(n *ast.SelectorExpr) (types.Object, bool)
	sel = func(n *ast.SelectorExpr) (types.Object, bool) {
		switch x := ast.Unparen(n.X).(type) {
		case *ast.SelectorExpr:
			return sel(x)
		case *ast.Ident:
			return id(x)
		}
		return nil, false
	}
	seen := map[types.Object]*runVariable{}
	firstUse := map[types.Object]token.Pos{}
	var order []types.Object
	ast.Inspect(block, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		if start <= n.Pos() && n.End() <= end {
			var obj types.Object
			var free, prune bool
			switch n := n.(type) {
			case *ast.BranchStmt:
				return false
			case *ast.Ident:
				obj, free = id(n)
			case *ast.SelectorExpr:
				obj, free = sel(n)
				prune = true
			}
			if obj != nil {
				seen[obj] = &runVariable{obj: obj, free: free}
				order = append(order, obj)
				if first, ok := firstUse[obj]; !ok || n.Pos() < first {
					firstUse[obj] = n.Pos()
				}
				if prune {
					return false
				}
			}
		}
		return n.Pos() <= end
	})
	ast.Inspect(block, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		if n.Pos() < start || n.End() > end {
			return n.Pos() <= end
		}
		switch n := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range n.Lhs {
				ident, ok := lhs.(*ast.Ident)
				if !ok {
					continue
				}
				obj, _ := id(ident)
				if obj == nil || seen[obj] == nil {
					continue
				}
				seen[obj].assigned = true
				if n.Tok != token.DEFINE || firstUse[obj] != ident.Pos() {
					continue
				}
				for _, rhs := range n.Rhs {
					if references(info, rhs, obj) {
						continue
					}
					seen[obj].defined = true
					break
				}
			}
			return false
		case *ast.DeclStmt:
			gen, ok := n.Decl.(*ast.GenDecl)
			if !ok {
				return false
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, name := range vs.Names {
					if obj, _ := id(name); obj != nil && seen[obj] != nil {
						seen[obj].assigned = true
					}
				}
			}
			return false
		case *ast.IncDecStmt:
			if ident, ok := n.X.(*ast.Ident); ok {
				if obj, _ := id(ident); obj != nil && seen[obj] != nil {
					seen[obj].assigned = true
				}
			}
			return false
		}
		return true
	})
	var out []*runVariable
	done := map[types.Object]bool{}
	for _, obj := range order {
		if done[obj] {
			continue
		}
		done[obj] = true
		var tn *types.TypeName
		switch t := obj.Type().(type) {
		case *types.Named:
			tn = t.Obj()
		case *types.Alias:
			tn = t.Obj()
		}
		if tn != nil {
			if tn.Pkg() != nil && tn.Parent() != tn.Pkg().Scope() && tn.Parent() != nil && !(start <= tn.Pos() && tn.Pos() <= end) {
				return nil, fmt.Errorf("the run uses a local type declared outside it")
			}
		}
		out = append(out, seen[obj])
	}
	return out, nil
}

// usedIn reports whether obj is used between start and end, and where first.
func usedIn(tp *typedPackage, start, end token.Pos, obj types.Object) (bool, *ast.Ident) {
	var first *ast.Ident
	for _, ident := range tp.usesOf(obj) {
		if ident.Pos() < start || ident.End() > end {
			continue
		}
		if first == nil || ident.Pos() < first.Pos() {
			first = ident
		}
	}
	return first != nil, first
}

// overridden reports whether the first use of obj after the run overwrites it
// without reading it, in which case the run's value is not needed.
func overridden(info *types.Info, firstUse *ast.Ident, obj types.Object, free bool, outer ast.Node) bool {
	result := false
	ast.Inspect(outer, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		if !free && assign.Tok == token.ASSIGN {
			return false
		}
		for _, lhs := range assign.Lhs {
			ident, ok := lhs.(*ast.Ident)
			if !ok || ident != firstUse || info.Uses[ident] != obj {
				continue
			}
			for _, rhs := range assign.Rhs {
				if references(info, rhs, obj) {
					return false
				}
			}
			result = true
			return false
		}
		return false
	})
	return result
}

func references(info *types.Info, expr ast.Expr, obj types.Object) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		if ident, ok := n.(*ast.Ident); ok && info.Uses[ident] == obj {
			found = true
		}
		return !found
	})
	return found
}

// returnedOuter is a variable declared outside the run that one of its
// returns hands back by name, or "" if there is none.
func returnedOuter(info *types.Info, returns []*ast.ReturnStmt, start, end token.Pos) string {
	for _, ret := range returns {
		for _, r := range ret.Results {
			id, ok := ast.Unparen(r).(*ast.Ident)
			if !ok {
				continue
			}
			if v, ok := info.Uses[id].(*types.Var); ok && !v.IsField() && v.Parent() != nil && v.Parent() != types.Universe &&
				(v.Pos() < start || v.Pos() > end) && v.Parent() != v.Pkg().Scope() {
				return id.Name
			}
		}
	}
	return ""
}

// fieldWrite is the variable, declared outside stmts, whose field or array
// element stmts write in place, or "" if there is none: m.S = 1, a[i]++.
func fieldWrite(info *types.Info, stmts []ast.Stmt) string {
	start, end := stmts[0].Pos(), stmts[len(stmts)-1].End()
	written := ""
	check := func(e ast.Expr) {
		if _, ok := ast.Unparen(e).(*ast.Ident); ok {
			return // the variable itself: the results model covers it
		}
		base := valueBase(info, e)
		if base == nil {
			return
		}
		if obj := info.Uses[base]; obj != nil && (obj.Pos() < start || obj.Pos() > end) && written == "" {
			written = base.Name
		}
	}
	for _, st := range stmts {
		ast.Inspect(st, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.FuncLit:
				return false
			case *ast.AssignStmt:
				for _, lhs := range n.Lhs {
					check(lhs)
				}
			case *ast.IncDecStmt:
				check(n.X)
			}
			return true
		})
	}
	return written
}

// valueBase is the variable e is part of, reached through struct fields and
// array elements held by value, or nil when a pointer, slice or map is in the
// way: writing through one of those reaches the caller's value either way.
func valueBase(info *types.Info, e ast.Expr) *ast.Ident {
	for {
		switch x := e.(type) {
		case *ast.ParenExpr:
			e = x.X
		case *ast.Ident:
			return x
		case *ast.SelectorExpr:
			sel := info.Selections[x]
			if sel == nil || sel.Kind() != types.FieldVal || sel.Indirect() {
				return nil
			}
			if t := info.TypeOf(x.X); t == nil || isPointer(t) {
				return nil
			}
			e = x.X
		case *ast.IndexExpr:
			t := info.TypeOf(x.X)
			if t == nil {
				return nil
			}
			if _, ok := t.Underlying().(*types.Array); !ok {
				return nil
			}
			e = x.X
		default:
			return nil
		}
	}
}

func isPointer(t types.Type) bool {
	_, ok := t.Underlying().(*types.Pointer)
	return ok
}

// aliased reports whether the run lets anything keep a reference to obj: its
// address taken with &, implicitly by a pointer-receiver method, or by a
// closure that captures it.
//
// A closure that mentions obj holds the variable, not its value, but that is
// only a difference when something writes obj: a copy nobody changes reads
// the same as the original. So a closure counts only when obj is written
// somewhere in its function. run() passes its stderr parameter to the
// closure that reports an interrupt, and that alone kept its whole
// subcommand where it was.
func aliased(info *types.Info, stmts []ast.Stmt, obj types.Object, written bool) bool {
	found := false
	is := func(x ast.Expr) bool {
		id, ok := ast.Unparen(x).(*ast.Ident)
		return ok && (info.Uses[id] == obj || info.Defs[id] == obj)
	}
	for _, s := range stmts {
		ast.Inspect(s, func(n ast.Node) bool {
			if found {
				return false
			}
			switch n := n.(type) {
			case *ast.UnaryExpr:
				found = n.Op == token.AND && is(n.X)
			case *ast.SelectorExpr:
				if is(n.X) {
					if sel, ok := info.Selections[n]; ok && sel.Kind() != types.FieldVal {
						if sig, ok := sel.Obj().Type().(*types.Signature); ok && sig.Recv() != nil {
							_, ptr := sig.Recv().Type().Underlying().(*types.Pointer)
							_, isPtr := obj.Type().Underlying().(*types.Pointer)
							found = ptr && !isPtr
						}
					}
				}
			case *ast.FuncLit:
				if !written {
					return true
				}
				ast.Inspect(n.Body, func(m ast.Node) bool {
					if id, ok := m.(*ast.Ident); ok && info.Uses[id] == obj {
						found = true
					}
					return !found
				})
				return false
			}
			return !found
		})
	}
	return found
}

// written reports whether anything in fn assigns obj, increments it or takes
// its address, after it is declared.
func written(info *types.Info, fn *ast.FuncDecl, obj types.Object) bool {
	is := func(x ast.Expr) bool {
		id, ok := ast.Unparen(x).(*ast.Ident)
		return ok && info.Uses[id] == obj
	}
	found := false
	ast.Inspect(fn, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range n.Lhs {
				if is(lhs) {
					found = true
				}
			}
		case *ast.IncDecStmt:
			found = found || is(n.X)
		case *ast.UnaryExpr:
			found = found || n.Op == token.AND && is(n.X)
		case *ast.RangeStmt:
			found = found || n.Key != nil && is(n.Key) || n.Value != nil && is(n.Value)
		}
		return !found
	})
	return found
}

// readLater reports whether obj can be read after the run by something other
// than the code that follows it: the next iteration of a loop inside obj's
// scope, or a closure.
func readLater(info *types.Info, outer *ast.FuncDecl, parent map[ast.Node]ast.Node, stmts []ast.Stmt, obj types.Object) bool {
	for n := parent[stmts[0]]; n != nil && obj.Parent().Contains(n.Pos()); n = parent[n] {
		switch n.(type) {
		case *ast.ForStmt, *ast.RangeStmt:
			return true
		}
	}
	captured := false
	ast.Inspect(outer, func(n ast.Node) bool {
		lit, ok := n.(*ast.FuncLit)
		if !ok || captured {
			return !captured
		}
		ast.Inspect(lit.Body, func(m ast.Node) bool {
			if id, ok := m.(*ast.Ident); ok && info.Uses[id] == obj {
				captured = true
			}
			return !captured
		})
		return false
	})
	return captured
}

// generic reports whether t mentions a type parameter.
func generic(t types.Type) bool {
	switch t := t.(type) {
	case *types.TypeParam:
		return true
	case *types.Pointer:
		return generic(t.Elem())
	case *types.Slice:
		return generic(t.Elem())
	case *types.Array:
		return generic(t.Elem())
	case *types.Chan:
		return generic(t.Elem())
	case *types.Map:
		return generic(t.Key()) || generic(t.Elem())
	case *types.Named:
		for a := range t.TypeArgs().Types() {
			if generic(a) {
				return true
			}
		}
	case *types.Signature:
		for _, tuple := range []*types.Tuple{t.Params(), t.Results()} {
			for v := range tuple.Variables() {
				if generic(v.Type()) {
					return true
				}
			}
		}
	case *types.Struct:
		for f := range t.Fields() {
			if generic(f.Type()) {
				return true
			}
		}
	}
	return false
}

// importDelta is how the imports change when the subtrees in gone leave the
// files and the ones in arrived land in them: an import nothing refers to any
// more is removed with its spec, and its declaration when that was the last
// spec; one the new code needs is added. Imports that were already unused are
// left out - the tree would not have built.
func importDelta(tp *typedPackage, files []*ast.File, gone map[ast.Node]bool, arrived map[*ast.File]map[string]bool) int {
	uses := func(n ast.Node, skip map[ast.Node]bool, into map[string]bool) {
		ast.Inspect(n, func(x ast.Node) bool {
			if skip[x] {
				return false
			}
			if id, ok := x.(*ast.Ident); ok {
				if pn, ok := tp.info.Uses[id].(*types.PkgName); ok {
					into[pn.Imported().Path()] = true
				}
			}
			return true
		})
	}
	delta := 0
	seen := map[*ast.File]bool{}
	for _, f := range files {
		if seen[f] {
			continue
		}
		seen[f] = true
		before, after := map[string]bool{}, map[string]bool{}
		uses(f, nil, before)
		uses(f, gone, after)
		for path := range arrived[f] {
			after[path] = true
		}
		imported := map[string]bool{}
		hasDecl := false
		for _, d := range f.Decls {
			g, ok := d.(*ast.GenDecl)
			if !ok || g.Tok != token.IMPORT {
				continue
			}
			hasDecl = true
			removed := 0
			for _, s := range g.Specs {
				spec := s.(*ast.ImportSpec)
				path, _ := strconv.Unquote(spec.Path.Value)
				imported[path] = true
				if spec.Name != nil && (spec.Name.Name == "_" || spec.Name.Name == ".") {
					continue
				}
				if before[path] && !after[path] {
					delta -= cost.Count(spec)
					removed++
				}
			}
			if removed == len(g.Specs) {
				delta-- // the declaration goes with its last spec
				hasDecl = false
			}
		}
		added := 0
		for path := range after {
			if !imported[path] {
				added++
			}
		}
		if added > 0 {
			delta += 2 * added
			if !hasDecl {
				delta++
			}
		}
	}
	return delta
}

// packagesIn is the import paths the nodes refer to.
func packagesIn(info *types.Info, nodes ...ast.Node) map[string]bool {
	out := map[string]bool{}
	for _, n := range nodes {
		ast.Inspect(n, func(x ast.Node) bool {
			if id, ok := x.(*ast.Ident); ok {
				if pn, ok := info.Uses[id].(*types.PkgName); ok {
					out[pn.Imported().Path()] = true
				}
			}
			return true
		})
	}
	return out
}

// copiesAgree reports whether every copy of a run means what the first one
// does. The copies are the same text, but the call gopls writes is worked
// out from the first copy alone and pasted over the others, so it is right
// for them only if each identifier refers to the same thing, or to a local of
// the same type, and each copy's surroundings need the same results.
//
// On cc-connect the same text declared "opts" as a Feishu options struct in
// one function and a Weixin one in the next, and a result the code after one
// copy read was never read after another, where := then declared a variable
// nothing used. Neither compiled.
func copiesAgree(tp *typedPackage, occ []Occurrence) error {
	idents := func(stmts []ast.Stmt) []*ast.Ident {
		var out []*ast.Ident
		for _, s := range stmts {
			ast.Inspect(s, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok && id.Name != "_" {
					out = append(out, id)
				}
				return true
			})
		}
		return out
	}
	object := func(id *ast.Ident) types.Object {
		if o := tp.info.Uses[id]; o != nil {
			return o
		}
		return tp.info.Defs[id]
	}
	local := func(o types.Object) bool {
		v, ok := o.(*types.Var)
		return ok && !v.IsField() && v.Parent() != nil && v.Parent() != tp.pkg.Scope()
	}
	first := idents(occ[0].stmts)
	needs := func(o Occurrence, ids []*ast.Ident) map[int]bool {
		out := map[int]bool{}
		end := o.stmts[len(o.stmts)-1].End()
		var outer *ast.FuncDecl
		for _, d := range o.file.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Pos() <= o.stmts[0].Pos() && end <= fn.End() {
				outer = fn
			}
		}
		for i, id := range ids {
			obj := object(id)
			if obj == nil || !local(obj) || obj.Parent() == nil {
				continue
			}
			used, firstUse := usedIn(tp, end, obj.Parent().End(), obj)
			if used && outer != nil && !overridden(tp.info, firstUse, obj, obj.Pos() < o.stmts[0].Pos(), outer) {
				out[i] = true
			}
		}
		return out
	}
	want := needs(occ[0], first)
	// A return inside the run is carried out by the caller, with plumbing
	// built from the first copy's enclosing function; at another copy it
	// has to return the same types.
	returns := false
	for _, s := range occ[0].stmts {
		ast.Inspect(s, func(n ast.Node) bool {
			switch n.(type) {
			case *ast.FuncLit:
				return false
			case *ast.ReturnStmt:
				returns = true
			}
			return !returns
		})
	}
	var firstSig *types.Signature
	if returns {
		firstSig = enclosingSignature(tp, occ[0])
	}
	for _, o := range occ[1:] {
		if returns {
			sig := enclosingSignature(tp, o)
			if firstSig == nil || sig == nil || !types.Identical(firstSig.Results(), sig.Results()) {
				return fmt.Errorf("the copies return from functions with different results")
			}
		}
		other := idents(o.stmts)
		if len(other) != len(first) {
			return fmt.Errorf("the copies differ in shape")
		}
		for i := range first {
			a, b := object(first[i]), object(other[i])
			switch {
			case (a == nil) != (b == nil):
				return fmt.Errorf("%s resolves in one copy and not another", first[i].Name)
			case a == nil:
			case local(a) != local(b):
				return fmt.Errorf("%s is local in one copy and not another", first[i].Name)
			case local(a):
				if !types.Identical(a.Type(), b.Type()) {
					return fmt.Errorf("%s is %s in one copy and %s in another", first[i].Name, a.Type(), b.Type())
				}
				// x, err := f() declares err in one copy and reuses an err
				// declared earlier in another; the := gopls writes for the
				// first then declares nothing new at the second.
				inA := occ[0].stmts[0].Pos() <= a.Pos() && a.Pos() < occ[0].stmts[len(occ[0].stmts)-1].End()
				inB := o.stmts[0].Pos() <= b.Pos() && b.Pos() < o.stmts[len(o.stmts)-1].End()
				if inA != inB {
					return fmt.Errorf("%s is declared by the run in one copy and before it in another", first[i].Name)
				}
			case a != b:
				return fmt.Errorf("%s refers to different things in the copies", first[i].Name)
			}
		}
		got := needs(o, other)
		for i := range first {
			if want[i] != got[i] {
				return fmt.Errorf("the code after the copies needs different results (%s)", first[i].Name)
			}
		}
	}
	return nil
}

// frameBound names a statement in the run whose meaning depends on the
// function it runs in: a defer, or a call to recover. Function literals
// inside the run have frames of their own and are not looked into.
func frameBound(info *types.Info, stmts []ast.Stmt) string {
	what := ""
	for _, s := range stmts {
		ast.Inspect(s, func(n ast.Node) bool {
			if what != "" {
				return false
			}
			switch n := n.(type) {
			case *ast.FuncLit:
				return false
			case *ast.DeferStmt:
				what = "a defer"
			case *ast.CallExpr:
				if id, ok := ast.Unparen(n.Fun).(*ast.Ident); ok {
					if b, ok := info.Uses[id].(*types.Builtin); ok && b.Name() == "recover" {
						what = "a call to recover"
					}
				}
			}
			return what == ""
		})
	}
	return what
}

// tailReturns reports whether stmts, a run in block, end by returning from
// the function around them, and that function's defers may move with them:
// its results are unnamed and nothing in the run calls recover.
func tailReturns(info *types.Info, parent map[ast.Node]ast.Node, outer *ast.FuncDecl, block ast.Node, stmts []ast.Stmt) bool {
	if _, ok := stmts[len(stmts)-1].(*ast.ReturnStmt); !ok {
		return false
	}
	sig := outer.Type
	for n := parent[block]; n != nil && n != outer; n = parent[n] {
		if lit, ok := n.(*ast.FuncLit); ok {
			sig = lit.Type
			break
		}
	}
	if sig.Results != nil {
		for _, f := range sig.Results.List {
			if len(f.Names) > 0 {
				return false
			}
		}
	}
	recovers := false
	for _, s := range stmts {
		ast.Inspect(s, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if id, ok := ast.Unparen(call.Fun).(*ast.Ident); ok {
					if b, ok := info.Uses[id].(*types.Builtin); ok && b.Name() == "recover" {
						recovers = true
					}
				}
			}
			return !recovers
		})
	}
	return !recovers
}

// enclosingSignature is the type of the innermost function, declared or
// literal, that the occurrence sits in.
func enclosingSignature(tp *typedPackage, o Occurrence) *types.Signature {
	pos := o.stmts[0].Pos()
	var t types.Type
	ast.Inspect(o.file, func(n ast.Node) bool {
		if n == nil || !(n.Pos() <= pos && pos < n.End()) {
			return false
		}
		switch f := n.(type) {
		case *ast.FuncDecl:
			// A declaration's type is its object's; the checker does not
			// record one for the FuncType syntax.
			if obj := tp.info.Defs[f.Name]; obj != nil {
				t = obj.Type()
			}
		case *ast.FuncLit:
			t = tp.info.TypeOf(f)
		}
		return true
	})
	sig, _ := t.(*types.Signature)
	return sig
}
