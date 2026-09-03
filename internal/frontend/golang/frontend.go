package golang

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"purgatrix/internal/sx"
)

const (
	FrontendID             = "go"
	FrontendVersion        = "go-v0"
	TranslationSpecVersion = "go-translation-v8"
)

func CompileFile(path, rootDir string) (*sx.Graph, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(rootDir, path)
	if err != nil {
		rel = path
	}
	rel = filepath.ToSlash(rel)
	pkgID := packageID(rel, file.Name.Name)
	b := &builder{
		fset:       fset,
		file:       file,
		path:       rel,
		modulePath: "local",
		packageID:  pkgID,
		graph: sx.Graph{
			Metadata: sx.Metadata{
				SchemaVersion:          sx.SchemaVersion,
				ModelVersion:           sx.ModelVersion,
				FrontendID:             FrontendID,
				FrontendVersion:        FrontendVersion,
				TranslationSpecVersion: TranslationSpecVersion,
				SourceUnitID:           rel,
			},
		},
		localRoots:  map[string]string{},
		stateByName: map[string]string{},
		externals:   map[string]string{},
	}
	b.predeclareRoots()
	b.compilePackageInit()
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		b.compileFunc(fn)
	}
	canon := sx.Canonical(&b.graph)
	if err := sx.Validate(&canon); err != nil {
		return nil, err
	}
	return &canon, nil
}

type builder struct {
	fset       *token.FileSet
	file       *ast.File
	path       string
	modulePath string
	packageID  string
	graph      sx.Graph

	rootID      string
	stateByName map[string]string
	localRoots  map[string]string
	externals   map[string]string
	seq         int
	edgeSeq     int
}

func (b *builder) predeclareRoots() {
	for _, decl := range b.file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		id := b.rootIDForFunc(fn)
		b.localRoots[fn.Name.Name] = id
	}
}

func (b *builder) compilePackageInit() {
	specs := b.packageVarSpecs()
	if !hasInitializedSpec(specs) {
		return
	}
	identity := fmt.Sprintf("%s/%s.<package_init:%s>", b.modulePath, b.packageID, b.path)
	root := b.addRoot("package_init", identity, b.source(b.file.Name.Pos(), b.file.Name.End()), 0, 0, 0)
	prevRoot := b.rootID
	prevState := b.stateByName
	b.rootID = root
	b.stateByName = map[string]string{}
	for _, vs := range specs {
		for _, name := range vs.Names {
			sid := b.addState("global", name.Name, root, b.source(name.Pos(), name.End()))
			b.stateByName[name.Name] = sid
		}
		if len(vs.Values) > 0 {
			nid := b.addNode("binding", root, b.source(vs.Pos(), vs.End()), nil)
			b.contain(root, nid, b.source(vs.Pos(), vs.End()))
			reads := b.readsFromExprs(vs.Values)
			for _, name := range vs.Names {
				lhs := b.stateByName[name.Name]
				b.write(nid, lhs, b.source(name.Pos(), name.End()))
				for _, r := range reads {
					b.edge("data_dependency", r, lhs, b.source(vs.Pos(), vs.End()), nil)
				}
			}
		}
	}
	b.rootID = prevRoot
	b.stateByName = prevState
}

func (b *builder) packageVarSpecs() []*ast.ValueSpec {
	var specs []*ast.ValueSpec
	for _, decl := range b.file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			if vs, ok := spec.(*ast.ValueSpec); ok {
				specs = append(specs, vs)
			}
		}
	}
	return specs
}

func hasInitializedSpec(specs []*ast.ValueSpec) bool {
	for _, spec := range specs {
		if len(spec.Values) > 0 {
			return true
		}
	}
	return false
}

func (b *builder) compileFunc(fn *ast.FuncDecl) {
	params := countFields(fn.Type.Params)
	results := countFields(fn.Type.Results)
	receivers := countFields(fn.Recv)
	root := b.rootIDForFunc(fn)
	identity := b.identityForFunc(fn)
	b.graph.Nodes = append(b.graph.Nodes, sx.Node{
		ID:     root,
		Kind:   "root",
		Source: b.source(fn.Pos(), fn.End()),
		Attributes: map[string]any{
			"root_kind":       rootKind(fn),
			"identity":        identity,
			"parameter_count": params,
			"receiver_count":  receivers,
			"result_count":    results,
		},
	})
	b.graph.Roots = append(b.graph.Roots, root)
	prevRoot := b.rootID
	prevState := b.stateByName
	b.rootID = root
	b.stateByName = map[string]string{}
	b.addFuncState(fn)
	if fn.Body != nil {
		b.compileStmtList(root, fn.Body.List)
	}
	b.rootID = prevRoot
	b.stateByName = prevState
}

func (b *builder) addFuncState(fn *ast.FuncDecl) {
	if fn.Recv != nil {
		for _, f := range fn.Recv.List {
			for _, name := range fieldNames(f, "recv") {
				b.stateByName[name] = b.addState("receiver", name, b.rootID, b.source(f.Pos(), f.End()))
			}
		}
	}
	if fn.Type.Params != nil {
		for _, f := range fn.Type.Params.List {
			for _, name := range fieldNames(f, "param") {
				b.stateByName[name] = b.addState("parameter", name, b.rootID, b.source(f.Pos(), f.End()))
			}
		}
	}
	if fn.Type.Results != nil {
		for _, f := range fn.Type.Results.List {
			for _, name := range fieldNames(f, "result") {
				if name != "_" {
					b.stateByName[name] = b.addState("result", name, b.rootID, b.source(f.Pos(), f.End()))
				}
			}
		}
	}
}

func (b *builder) compileStmtList(parent string, stmts []ast.Stmt) {
	for _, stmt := range stmts {
		b.compileStmt(parent, stmt)
	}
}

func (b *builder) compileStmt(parent string, stmt ast.Stmt) {
	switch s := stmt.(type) {
	case *ast.IfStmt:
		b.compileIf(parent, s)
	case *ast.ForStmt:
		b.compileFor(parent, s)
	case *ast.RangeStmt:
		b.compileRange(parent, s)
	case *ast.SwitchStmt:
		b.compileSwitch(parent, s)
	case *ast.TypeSwitchStmt:
		b.compileTypeSwitch(parent, s)
	case *ast.ReturnStmt:
		nid := b.structWithReads("return", parent, s, s.Results)
		_ = nid
	case *ast.AssignStmt:
		b.compileAssign(parent, s)
	case *ast.DeclStmt:
		b.compileDecl(parent, s)
	case *ast.ExprStmt:
		if call, ok := s.X.(*ast.CallExpr); ok {
			b.compileCall(parent, call, "call")
		} else {
			b.structWithReads("operation", parent, s, []ast.Expr{s.X})
		}
	case *ast.DeferStmt:
		b.compileCall(parent, s.Call, "defer")
	case *ast.GoStmt:
		b.compileCall(parent, s.Call, "concurrent_call")
	case *ast.BranchStmt:
		b.addContained("jump", parent, b.source(s.Pos(), s.End()), nil)
	case *ast.IncDecStmt:
		nid := b.addContained("assignment", parent, b.source(s.Pos(), s.End()), nil)
		for _, r := range b.readsFromExprs([]ast.Expr{s.X}) {
			b.read(nid, r, b.source(s.X.Pos(), s.X.End()))
			b.write(nid, r, b.source(s.X.Pos(), s.X.End()))
		}
	case *ast.BlockStmt:
		b.compileStmtList(parent, s.List)
	case *ast.LabeledStmt:
		// The label is a jump target, not work of its own. Falling through to
		// the default case would collapse the whole labelled statement - loop,
		// body and every read inside it - into one opaque operation node.
		b.compileStmt(parent, s.Stmt)
	case *ast.SelectStmt:
		b.compileSelect(parent, s)
	case *ast.SendStmt:
		b.structWithReads("operation", parent, s, []ast.Expr{s.Chan, s.Value})
	default:
		b.addContained("operation", parent, b.source(stmt.Pos(), stmt.End()), nil)
	}
}

// compileSelect models a select as a multi-way branch over its communication
// clauses, mirroring how switch is modelled.
func (b *builder) compileSelect(parent string, s *ast.SelectStmt) {
	nid := b.addContained("multi_branch", parent, b.source(s.Pos(), s.End()), nil)
	if s.Body == nil {
		return
	}
	for _, clause := range s.Body.List {
		cc, ok := clause.(*ast.CommClause)
		if !ok {
			continue
		}
		label := "comm"
		if cc.Comm == nil {
			label = "default"
		}
		caseID := b.addContained("case", nid, b.source(cc.Pos(), cc.End()), map[string]any{"label": label})
		if cc.Comm != nil {
			b.compileStmt(caseID, cc.Comm)
		}
		b.compileStmtList(caseID, cc.Body)
	}
}

func (b *builder) compileIf(parent string, s *ast.IfStmt) {
	nid := b.addContained("branch", parent, b.source(s.Pos(), s.End()), nil)
	// if x := f(); cond - the init statement is real work and its reads are
	// real reads. Dropping it loses both from the graph.
	if s.Init != nil {
		b.compileStmt(nid, s.Init)
	}
	for _, r := range b.readsFromExprs([]ast.Expr{s.Cond}) {
		b.read(nid, r, b.source(s.Cond.Pos(), s.Cond.End()))
	}
	thenCase := b.addContained("case", nid, b.source(s.Body.Pos(), s.Body.End()), map[string]any{"label": "then"})
	b.compileStmtList(thenCase, s.Body.List)
	if s.Else == nil {
		implicit := b.addNode("case", b.rootID, syntheticSource(b.source(s.Pos(), s.End()), "implicit_else_case"), map[string]any{"label": "else"})
		b.contain(nid, implicit, syntheticSource(b.source(s.Pos(), s.End()), "implicit_else_case"))
		return
	}
	elseCase := b.addContained("case", nid, b.source(s.Else.Pos(), s.Else.End()), map[string]any{"label": "else"})
	b.compileStmt(elseCase, s.Else)
}

func (b *builder) compileFor(parent string, s *ast.ForStmt) {
	nid := b.addContained("loop", parent, b.source(s.Pos(), s.End()), nil)
	if s.Init != nil {
		b.compileStmt(nid, s.Init)
	}
	if s.Post != nil {
		b.compileStmt(nid, s.Post)
	}
	if s.Cond != nil {
		for _, r := range b.readsFromExprs([]ast.Expr{s.Cond}) {
			b.read(nid, r, b.source(s.Cond.Pos(), s.Cond.End()))
		}
	}
	b.compileStmtList(nid, s.Body.List)
}

func (b *builder) compileRange(parent string, s *ast.RangeStmt) {
	nid := b.addContained("loop", parent, b.source(s.Pos(), s.End()), nil)
	for _, r := range b.readsFromExprs([]ast.Expr{s.X}) {
		b.read(nid, r, b.source(s.X.Pos(), s.X.End()))
	}
	for _, expr := range []ast.Expr{s.Key, s.Value} {
		if id, ok := expr.(*ast.Ident); ok && id.Name != "_" {
			sid := b.ensureLocal(id.Name, b.source(id.Pos(), id.End()))
			b.write(nid, sid, b.source(id.Pos(), id.End()))
		}
	}
	b.compileStmtList(nid, s.Body.List)
}

func (b *builder) compileSwitch(parent string, s *ast.SwitchStmt) {
	nid := b.addContained("multi_branch", parent, b.source(s.Pos(), s.End()), nil)
	if s.Init != nil {
		b.compileStmt(nid, s.Init)
	}
	if s.Tag != nil {
		for _, r := range b.readsFromExprs([]ast.Expr{s.Tag}) {
			b.read(nid, r, b.source(s.Tag.Pos(), s.Tag.End()))
		}
	}
	for _, stmt := range s.Body.List {
		cc, ok := stmt.(*ast.CaseClause)
		if !ok {
			continue
		}
		cid := b.addContained("case", nid, b.source(cc.Pos(), cc.End()), nil)
		for _, r := range b.readsFromExprs(cc.List) {
			b.read(cid, r, b.source(cc.Pos(), cc.End()))
		}
		b.compileStmtList(cid, cc.Body)
	}
}

func (b *builder) compileTypeSwitch(parent string, s *ast.TypeSwitchStmt) {
	nid := b.addContained("multi_branch", parent, b.source(s.Pos(), s.End()), nil)
	if s.Init != nil {
		b.compileStmt(nid, s.Init)
	}
	if s.Assign != nil {
		b.compileStmt(nid, s.Assign)
	}
	for _, stmt := range s.Body.List {
		cc, ok := stmt.(*ast.CaseClause)
		if !ok {
			continue
		}
		cid := b.addContained("case", nid, b.source(cc.Pos(), cc.End()), nil)
		b.compileStmtList(cid, cc.Body)
	}
}

func (b *builder) compileAssign(parent string, s *ast.AssignStmt) {
	kind := "assignment"
	if s.Tok == token.DEFINE {
		kind = "binding"
	}
	nid := b.addContained(kind, parent, b.source(s.Pos(), s.End()), nil)
	reads := b.readsFromExprs(s.Rhs)
	for _, r := range reads {
		b.read(nid, r, b.source(s.Pos(), s.End()))
	}
	for _, lhs := range s.Lhs {
		// m[k] = v writes m and reads k. Without this the index is a value the
		// graph never sees read, which reads back as dead state.
		for _, r := range b.readsFromExprs(indexExprs(lhs)) {
			b.read(nid, r, b.source(lhs.Pos(), lhs.End()))
		}
		name := stateName(lhs)
		if name == "" || name == "_" {
			continue
		}
		var sid string
		if s.Tok == token.DEFINE {
			sid = b.ensureLocal(name, b.source(lhs.Pos(), lhs.End()))
		} else {
			sid = b.ensureState(name, b.source(lhs.Pos(), lhs.End()))
		}
		if _, direct := lhs.(*ast.Ident); direct {
			b.write(nid, sid, b.source(lhs.Pos(), lhs.End()))
		} else {
			b.writeThrough(nid, sid, b.source(lhs.Pos(), lhs.End()))
		}
		for _, r := range reads {
			b.edge("data_dependency", r, sid, b.source(s.Pos(), s.End()), nil)
		}
	}
}

func (b *builder) compileDecl(parent string, s *ast.DeclStmt) {
	gen, ok := s.Decl.(*ast.GenDecl)
	if !ok {
		b.addContained("operation", parent, b.source(s.Pos(), s.End()), nil)
		return
	}
	for _, spec := range gen.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		nid := b.addContained("binding", parent, b.source(vs.Pos(), vs.End()), nil)
		reads := b.readsFromExprs(vs.Values)
		for _, r := range reads {
			b.read(nid, r, b.source(vs.Pos(), vs.End()))
		}
		for _, name := range vs.Names {
			if name.Name == "_" {
				continue
			}
			sid := b.ensureLocal(name.Name, b.source(name.Pos(), name.End()))
			b.write(nid, sid, b.source(name.Pos(), name.End()))
			for _, r := range reads {
				b.edge("data_dependency", r, sid, b.source(vs.Pos(), vs.End()), nil)
			}
		}
	}
}

func (b *builder) structWithReads(kind, parent string, stmt ast.Stmt, exprs []ast.Expr) string {
	nid := b.addContained(kind, parent, b.source(stmt.Pos(), stmt.End()), nil)
	for _, r := range b.readsFromExprs(exprs) {
		b.read(nid, r, b.source(stmt.Pos(), stmt.End()))
	}
	return nid
}

func (b *builder) compileCall(parent string, call *ast.CallExpr, kind string) string {
	nid := b.addContained(kind, parent, b.source(call.Pos(), call.End()), nil)
	reads := call.Args
	// x.method(args) reads x. Counting only the arguments loses the receiver,
	// and a variable used solely as a receiver then looks written but unread.
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		reads = append([]ast.Expr{sel.X}, reads...)
	}
	for _, r := range b.readsFromExprs(reads) {
		b.read(nid, r, b.source(call.Pos(), call.End()))
	}
	target, resolution := b.callTarget(call)
	b.edge("call", nid, target, b.source(call.Pos(), call.End()), map[string]any{"resolution": resolution})
	return nid
}

func (b *builder) callTarget(call *ast.CallExpr) (string, string) {
	name := callName(call.Fun)
	if name == "" {
		return b.externalRoot("unresolved", b.source(call.Pos(), call.End())), "unresolved"
	}
	if id, ok := b.localRoots[name]; ok {
		return id, "resolved"
	}
	return b.externalRoot(name, b.source(call.Pos(), call.End())), "external"
}

func (b *builder) readsFromExprs(exprs []ast.Expr) []string {
	seen := map[string]bool{}
	var ids []string
	var visit func(ast.Node)
	visit = func(n ast.Node) {
		switch x := n.(type) {
		case nil:
			return
		case *ast.Ident:
			if x.Name == "_" || isBuiltinOrKeyword(x.Name) {
				return
			}
			sid := b.ensureState(x.Name, b.source(x.Pos(), x.End()))
			if !seen[sid] {
				seen[sid] = true
				ids = append(ids, sid)
			}
			return
		case *ast.SelectorExpr:
			// x.Sel names a field or a package member, not a value in scope.
			// Visiting it mints a state node named after the field, which then
			// collides with every other use of that field name in the root.
			visit(x.X)
			return
		case *ast.CompositeLit:
			// The type of map[string]bool{} is not a read of `string` or `bool`.
			for _, elt := range x.Elts {
				visit(elt)
			}
			return
		case *ast.CallExpr:
			// make([]T, n) and new(T) carry a type in argument position.
			args := x.Args
			if id, ok := x.Fun.(*ast.Ident); ok && (id.Name == "make" || id.Name == "new") && len(args) > 0 {
				args = args[1:]
			}
			visit(x.Fun)
			for _, a := range args {
				visit(a)
			}
			return
		case *ast.ArrayType, *ast.MapType, *ast.ChanType, *ast.StructType,
			*ast.InterfaceType, *ast.FuncType, *ast.StarExpr, *ast.Ellipsis:
			// Pure type syntax reads nothing.
			return
		case *ast.TypeAssertExpr:
			visit(x.X)
			return
		}
		ast.Inspect(n, func(child ast.Node) bool {
			if child == nil || child == n {
				return child == n
			}
			visit(child)
			return false
		})
	}
	for _, expr := range exprs {
		visit(expr)
	}
	sort.Strings(ids)
	return ids
}

// ensureState resolves a name to its state node, inventing a root-local one
// for a name this root never declared. Such a name is almost always declared
// elsewhere - a package-level variable, or an import - so the node is marked
// undeclared: within this root the graph cannot see all of its uses.
func (b *builder) ensureState(name string, src sx.Source) string {
	if sid, ok := b.stateByName[name]; ok {
		return sid
	}
	sid := b.ensureLocal(name, src)
	for i := range b.graph.Nodes {
		if b.graph.Nodes[i].ID == sid {
			if b.graph.Nodes[i].Attributes == nil {
				b.graph.Nodes[i].Attributes = map[string]any{}
			}
			b.graph.Nodes[i].Attributes["declared_in_root"] = false
			break
		}
	}
	return sid
}

func (b *builder) ensureLocal(name string, src sx.Source) string {
	if sid, ok := b.stateByName[name]; ok {
		return sid
	}
	sid := b.addState("local", name, b.rootID, src)
	b.stateByName[name] = sid
	return sid
}

func (b *builder) addState(kind, name, rootID string, src sx.Source) string {
	id := b.newID("state", name)
	b.graph.Nodes = append(b.graph.Nodes, sx.Node{
		ID:     id,
		Kind:   kind,
		RootID: rootID,
		Source: src,
		Attributes: map[string]any{
			"name":          name,
			"owner_root_id": rootID,
		},
	})
	return id
}

func (b *builder) addRoot(rootKind, identity string, src sx.Source, params, receivers, results int) string {
	id := "root:" + sanitize(identity)
	b.graph.Nodes = append(b.graph.Nodes, sx.Node{
		ID:     id,
		Kind:   "root",
		Source: src,
		Attributes: map[string]any{
			"root_kind":       rootKind,
			"identity":        identity,
			"parameter_count": params,
			"receiver_count":  receivers,
			"result_count":    results,
		},
	})
	b.graph.Roots = append(b.graph.Roots, id)
	return id
}

func (b *builder) externalRoot(name string, src sx.Source) string {
	key := "external:" + name
	if id, ok := b.externals[key]; ok {
		return id
	}
	id := "root:external:" + sanitize(name)
	b.externals[key] = id
	b.graph.Nodes = append(b.graph.Nodes, sx.Node{
		ID:     id,
		Kind:   "root",
		Source: syntheticSource(src, "external_or_unresolved_call_target"),
		Attributes: map[string]any{
			"root_kind":       "external_root",
			"identity":        "external:" + name,
			"parameter_count": 0,
			"receiver_count":  0,
			"result_count":    0,
		},
	})
	b.graph.Roots = append(b.graph.Roots, id)
	return id
}

func (b *builder) addContained(kind, parent string, src sx.Source, attrs map[string]any) string {
	id := b.addNode(kind, b.rootID, src, attrs)
	b.contain(parent, id, src)
	return id
}

func (b *builder) addNode(kind, rootID string, src sx.Source, attrs map[string]any) string {
	id := b.newID(kind, "")
	b.graph.Nodes = append(b.graph.Nodes, sx.Node{ID: id, Kind: kind, RootID: rootID, Source: src, Attributes: attrs})
	return id
}

func (b *builder) contain(from, to string, src sx.Source) {
	b.edge("containment", from, to, src, nil)
}

func (b *builder) read(from, to string, src sx.Source) {
	b.edge("read", from, to, src, nil)
}

func (b *builder) write(from, to string, src sx.Source) {
	b.edge("write", from, to, src, nil)
}

// writeThrough records a write reaching a variable through a selector or an
// index, as in n.Field = v or m[k] = v. The variable itself is not replaced,
// so a consumer must not read this as "the variable is only ever assigned".
func (b *builder) writeThrough(from, to string, src sx.Source) {
	b.edge("write", from, to, src, map[string]any{"target": "indirect"})
}

func (b *builder) edge(kind, from, to string, src sx.Source, attrs map[string]any) {
	b.edgeSeq++
	b.graph.Edges = append(b.graph.Edges, sx.Edge{
		ID:         fmt.Sprintf("edge:%06d:%s", b.edgeSeq, kind),
		Kind:       kind,
		From:       from,
		To:         to,
		Source:     src,
		Attributes: attrs,
	})
}

func (b *builder) newID(kind, name string) string {
	b.seq++
	parts := []string{b.path, b.rootID, kind, fmt.Sprintf("%06d", b.seq)}
	if name != "" {
		parts = append(parts, name)
	}
	return "node:" + sanitize(strings.Join(parts, ":"))
}

// packageID identifies a package by its directory relative to the module root.
// The bare package name cannot identify a package: a repository with several
// `main` packages, or a `config` package in two directories, would emit the
// same root identity for distinct functions and fail to merge.
func packageID(relPath, packageName string) string {
	dir := path.Dir(relPath)
	if dir == "." || dir == "" || dir == "/" {
		return packageName
	}
	return dir
}

func (b *builder) rootIDForFunc(fn *ast.FuncDecl) string {
	return "root:" + sanitize(b.identityForFunc(fn))
}

func (b *builder) identityForFunc(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fmt.Sprintf("%s/%s.%s", b.modulePath, b.packageID, fn.Name.Name)
	}
	return fmt.Sprintf("%s/%s.%s.%s", b.modulePath, b.packageID, receiverName(fn.Recv.List[0].Type), fn.Name.Name)
}

func (b *builder) source(start, end token.Pos) sx.Source {
	sp := b.fset.Position(start)
	ep := b.fset.Position(end)
	return sx.Source{
		Path:        b.path,
		StartLine:   sp.Line,
		StartColumn: sp.Column,
		EndLine:     ep.Line,
		EndColumn:   ep.Column,
	}
}

func syntheticSource(src sx.Source, reason string) sx.Source {
	src.Synthetic = true
	src.SyntheticReason = reason
	return src
}

func countFields(fl *ast.FieldList) int {
	if fl == nil {
		return 0
	}
	count := 0
	for _, f := range fl.List {
		if len(f.Names) == 0 {
			count++
		} else {
			count += len(f.Names)
		}
	}
	return count
}

func fieldNames(f *ast.Field, prefix string) []string {
	if len(f.Names) == 0 {
		return []string{prefix}
	}
	names := make([]string, 0, len(f.Names))
	for _, n := range f.Names {
		names = append(names, n.Name)
	}
	return names
}

func rootKind(fn *ast.FuncDecl) string {
	if fn.Recv != nil {
		return "method"
	}
	return "function"
}

func receiverName(expr ast.Expr) string {
	switch x := expr.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.StarExpr:
		return receiverName(x.X)
	default:
		return "receiver"
	}
}

// indexExprs returns the subscript expressions of an assignment target, which
// are read even though the target itself is written.
func indexExprs(expr ast.Expr) []ast.Expr {
	var out []ast.Expr
	for {
		switch x := expr.(type) {
		case *ast.IndexExpr:
			out = append(out, x.Index)
			expr = x.X
		case *ast.SelectorExpr:
			expr = x.X
		case *ast.StarExpr:
			expr = x.X
		default:
			return out
		}
	}
}

func stateName(expr ast.Expr) string {
	switch x := expr.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		// Writing n.Field writes n. Naming the target "n.Field" makes it a
		// different node from the one reads resolve to, and state nodes are
		// root-local, so a field written in one method and read in another
		// never meets its reads at all. Field granularity is not recoverable
		// without type information, and pretending to have it is worse than
		// not having it: analysis reads the write-only half as dead.
		return stateName(x.X)
	case *ast.IndexExpr:
		// Writing m[k] writes to m. Giving the indexed form its own name mints
		// a second state node for one variable: it collects the writes while
		// the base collects the reads, so the element node looks written but
		// never read, and the base looks read but never written. Both halves
		// are wrong, and analysis over the graph reads them as dead state.
		return stateName(x.X)
	default:
		return ""
	}
}

func callName(expr ast.Expr) string {
	switch x := expr.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		base := stateName(x.X)
		if base == "" {
			return x.Sel.Name
		}
		return base + "." + x.Sel.Name
	default:
		return ""
	}
}

func isBuiltinOrKeyword(name string) bool {
	switch name {
	case "nil", "true", "false", "iota", "len", "cap", "append", "copy", "delete", "make", "new", "panic", "recover", "print", "println", "complex", "real", "imag", "close":
		return true
	default:
		return false
	}
}

var unsafeID = regexp.MustCompile(`[^A-Za-z0-9_.:/-]+`)

func sanitize(s string) string {
	s = unsafeID.ReplaceAllString(s, "_")
	s = strings.ReplaceAll(s, "/", "_")
	return s
}
