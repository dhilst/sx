package analyze

import (
	"fmt"

	"purgatrix/internal/sc"
	"purgatrix/internal/sx"
)

// Rewrite applies a plan to the graph and returns the result. The rewrite is
// structural only: it models what the corresponding source edit does to the
// graph, so the caller can rescore and learn the exact saving instead of
// estimating it from weights.
//
// A rewritten graph is not a program. It exists to be scored.
func Rewrite(g *sx.Graph, p Plan) (*sx.Graph, error) {
	switch p.Kind {
	case "dead_state":
		return dropState(g, p.NodeID)
	case "guard_inversion":
		return liftElseArm(g, p.NodeID)
	case "excess_arity":
		return trimArity(g, p.NodeID, sx.DefaultHyperparameters().ExcessArityThreshold)
	case "duplicate_structure":
		return foldDuplicates(g, p)
	default:
		return nil, fmt.Errorf("no rewrite for plan kind %q", p.Kind)
	}
}

// ExactSaving scores the graph before and after the rewrite. A positive result
// is the number of points the scorer stops charging.
func ExactSaving(g *sx.Graph, p Plan) (int, error) {
	before, err := Verify(g)
	if err != nil {
		return 0, err
	}
	return exactSavingFrom(g, before, p)
}

// exactSavingFrom reuses a score the caller already has. Scoring the starting
// graph once per plan is the same answer computed N times.
func exactSavingFrom(g *sx.Graph, before int, p Plan) (int, error) {
	rewritten, err := Rewrite(g, p)
	if err != nil {
		return 0, err
	}
	after, err := Verify(rewritten)
	if err != nil {
		return 0, fmt.Errorf("rewritten graph did not score: %w", err)
	}
	return before - after, nil
}

// dropState removes a state node and every edge that touched it, which is what
// deleting an unread variable does to the graph.
func dropState(g *sx.Graph, nodeID string) (*sx.Graph, error) {
	if _, ok := newIndex(g).nodes[nodeID]; !ok {
		return nil, fmt.Errorf("node %q is not in the graph", nodeID)
	}
	out := clone(g)
	out.Nodes = out.Nodes[:0]
	for _, n := range g.Nodes {
		if n.ID != nodeID {
			out.Nodes = append(out.Nodes, n)
		}
	}
	out.Edges = out.Edges[:0]
	for _, e := range g.Edges {
		if e.From != nodeID && e.To != nodeID {
			out.Edges = append(out.Edges, e)
		}
	}
	return finish(out)
}

// liftElseArm models turning
//
//	if cond { terminal } else { work }
//
// into
//
//	if cond { terminal }
//	work
//
// The heavy case node disappears, its children re-attach to whatever contained
// the branch, and the branch - now without an else - gains the synthetic else
// case the frontend emits for a one-armed if.
func liftElseArm(g *sx.Graph, branchID string) (*sx.Graph, error) {
	ix := newIndex(g)
	branch, ok := ix.nodes[branchID]
	if !ok || branch.Kind != "branch" {
		return nil, fmt.Errorf("node %q is not a branch", branchID)
	}
	children := ix.children_(branchID)
	var cases []sx.Node
	for _, c := range children {
		if c.Kind == "case" {
			cases = append(cases, c)
		}
	}
	if len(cases) != 2 {
		return nil, fmt.Errorf("branch %q does not have exactly two cases", branchID)
	}
	weights := [2]int{ix.subtreeWeight(cases[0].ID), ix.subtreeWeight(cases[1].ID)}
	heavy := cases[0]
	if weights[1] > weights[0] {
		heavy = cases[1]
	}
	parent, ok := ix.parent[branchID]
	if !ok {
		return nil, fmt.Errorf("branch %q has no container", branchID)
	}

	out := clone(g)
	out.Nodes = out.Nodes[:0]
	for _, n := range g.Nodes {
		if n.ID != heavy.ID {
			out.Nodes = append(out.Nodes, n)
		}
	}
	// The one-armed if still has an else in the graph, synthesised.
	out.Nodes = append(out.Nodes, sx.Node{
		ID:     heavy.ID + ":synthetic-else",
		Kind:   "case",
		RootID: heavy.RootID,
		Source: sx.Source{
			Path: branch.Source.Path, StartLine: branch.Source.StartLine,
			StartColumn: branch.Source.StartColumn, EndLine: branch.Source.EndLine,
			EndColumn: branch.Source.EndColumn,
			Synthetic: true, SyntheticReason: "implicit_else_case",
		},
		Attributes: map[string]any{"label": "else"},
	})

	out.Edges = out.Edges[:0]
	for _, e := range g.Edges {
		switch {
		case e.From == heavy.ID && e.Kind == "containment":
			// The arm's statements now sit where the branch sits.
			lifted := e
			lifted.From = parent
			out.Edges = append(out.Edges, lifted)
		case e.From == heavy.ID || e.To == heavy.ID:
			// Drop the containment that attached the arm to the branch.
		default:
			out.Edges = append(out.Edges, e)
		}
	}
	out.Edges = append(out.Edges, sx.Edge{
		ID:     heavy.ID + ":synthetic-else-containment",
		Kind:   "containment",
		From:   branchID,
		To:     heavy.ID + ":synthetic-else",
		Source: branch.Source,
	})
	return finish(out)
}

// trimArity models folding surplus parameters and results into a struct that
// is already in scope, which leaves the root but drops the arity charge.
func trimArity(g *sx.Graph, rootID string, threshold int) (*sx.Graph, error) {
	root, ok := newIndex(g).nodes[rootID]
	if !ok || root.Kind != "root" {
		return nil, fmt.Errorf("node %q is not a root", rootID)
	}
	out := clone(g)
	out.Nodes = out.Nodes[:0]
	for _, n := range g.Nodes {
		if n.ID == rootID {
			attrs := map[string]any{}
			for k, v := range n.Attributes {
				attrs[k] = v
			}
			if attrInt(n, "parameter_count") > threshold {
				attrs["parameter_count"] = threshold
			}
			if attrInt(n, "result_count") > threshold {
				attrs["result_count"] = threshold
			}
			n.Attributes = attrs
		}
		out.Nodes = append(out.Nodes, n)
	}
	return finish(out)
}

// foldDuplicates models extracting duplicated structure into one function and
// calling it from each site.
//
// The first occurrence becomes the body of a new root, so it leaves the nesting
// it sat in - which is most of the win, since structural cost multiplies by
// depth. Every occurrence, including the first, is replaced by a call node and
// a call edge. The new root takes a parameter for each distinct value the body
// touched, because a shared function has to be given what it works on, and that
// arity is charged like any other.
func foldDuplicates(g *sx.Graph, p Plan) (*sx.Graph, error) {
	if len(p.group) < 2 {
		return nil, fmt.Errorf("duplicate plan has %d occurrences", len(p.group))
	}
	keep := p.group[0]
	body := map[string]bool{}
	for _, id := range keep.nodes {
		body[id] = true
	}
	doomed := map[string]bool{}
	for _, occ := range p.group[1:] {
		for _, id := range occ.nodes {
			doomed[id] = true
		}
	}

	ix := newIndex(g)
	anchor, ok := ix.nodes[keep.nodes[0]]
	if !ok {
		return nil, fmt.Errorf("anchor %q is missing", keep.nodes[0])
	}
	rootID := "root:extracted:" + p.Anchor
	newRoot := sx.Node{
		ID:   rootID,
		Kind: "root",
		Source: sx.Source{
			Path: anchor.Source.Path, StartLine: anchor.Source.StartLine,
			StartColumn: anchor.Source.StartColumn, EndLine: anchor.Source.EndLine,
			EndColumn: anchor.Source.EndColumn,
			Synthetic: true, SyntheticReason: "extracted_duplicate",
		},
		Attributes: map[string]any{
			"root_kind": "function",
			"identity":  "local/extracted." + p.Anchor,
		},
	}

	// What the body touches becomes its parameters.
	touched := map[string]bool{}
	for _, e := range g.Edges {
		if (e.Kind == "read" || e.Kind == "write") && body[e.From] {
			touched[e.To] = true
		}
	}
	newRoot.Attributes["parameter_count"] = len(touched)
	newRoot.Attributes["receiver_count"] = 0
	newRoot.Attributes["result_count"] = 1

	out := clone(g)
	out.Nodes = out.Nodes[:0]
	for _, n := range g.Nodes {
		if doomed[n.ID] {
			continue
		}
		if body[n.ID] {
			n.RootID = rootID
		}
		out.Nodes = append(out.Nodes, n)
	}
	params := map[string]string{}
	i := 0
	for sid := range touched {
		pid := rootID + ":param:" + itoa(i)
		params[sid] = pid
		out.Nodes = append(out.Nodes, sx.Node{
			ID: pid, Kind: "parameter", RootID: rootID, Source: newRoot.Source,
			Attributes: map[string]any{"name": "p" + itoa(i)},
		})
		i++
	}
	out.Nodes = append(out.Nodes, newRoot)
	out.Roots = append(out.Roots, rootID)

	callSites := map[string]sx.Node{}
	for _, occ := range p.group {
		n, ok := ix.nodes[occ.nodes[0]]
		if !ok {
			return nil, fmt.Errorf("occurrence %q is missing", occ.nodes[0])
		}
		callSites[occ.nodes[0]] = n
	}

	out.Edges = out.Edges[:0]
	for _, e := range g.Edges {
		switch {
		case doomed[e.From] || doomed[e.To]:
			// The removed copies take their edges with them.
		case e.Kind == "containment" && e.To == keep.nodes[0]:
			// The kept body now hangs off the new root.
			lifted := e
			lifted.From = rootID
			out.Edges = append(out.Edges, lifted)
		case (e.Kind == "read" || e.Kind == "write") && body[e.From]:
			// The body works on its parameters now.
			rebound := e
			rebound.To = params[e.To]
			out.Edges = append(out.Edges, rebound)
		default:
			out.Edges = append(out.Edges, e)
		}
	}

	// Each site becomes a call to the extracted root.
	n := 0
	for id, site := range callSites {
		parent, ok := ix.parent[id]
		if !ok {
			continue
		}
		if doomed[parent] {
			continue
		}
		callID := rootID + ":call:" + itoa(n)
		out.Nodes = append(out.Nodes, sx.Node{
			ID: callID, Kind: "call", RootID: site.RootID, Source: site.Source,
		})
		out.Edges = append(out.Edges,
			sx.Edge{ID: callID + ":in", Kind: "containment", From: parent, To: callID, Source: site.Source},
			sx.Edge{ID: callID + ":to", Kind: "call", From: callID, To: rootID, Source: site.Source,
				Attributes: map[string]any{"resolution": "resolved"}})
		n++
	}
	return finish(out)
}

func clone(g *sx.Graph) *sx.Graph {
	out := &sx.Graph{
		Metadata: g.Metadata,
		Nodes:    make([]sx.Node, 0, len(g.Nodes)),
		Edges:    make([]sx.Edge, 0, len(g.Edges)),
		Roots:    append([]string(nil), g.Roots...),
	}
	out.Nodes = append(out.Nodes, g.Nodes...)
	out.Edges = append(out.Edges, g.Edges...)
	return out
}

// finish canonicalises and validates, so a rewrite that produced an impossible
// graph is reported instead of scored.
func finish(g *sx.Graph) (*sx.Graph, error) {
	canon := sx.Canonical(g)
	if err := sx.Validate(&canon); err != nil {
		return nil, fmt.Errorf("rewrite produced an invalid graph: %w", err)
	}
	return &canon, nil
}

// index is built once per rewrite. The helpers below used to scan every node
// or edge per lookup, which is quadratic on a repository-sized graph and was
// the reason a full pass took the best part of a minute.
type index struct {
	nodes    map[string]sx.Node
	children map[string][]string
	parent   map[string]string
}

func newIndex(g *sx.Graph) *index {
	ix := &index{
		nodes:    make(map[string]sx.Node, len(g.Nodes)),
		children: map[string][]string{},
		parent:   map[string]string{},
	}
	for _, n := range g.Nodes {
		ix.nodes[n.ID] = n
	}
	for _, e := range g.Edges {
		if e.Kind == "containment" {
			ix.children[e.From] = append(ix.children[e.From], e.To)
			ix.parent[e.To] = e.From
		}
	}
	return ix
}

func findNode(g *sx.Graph, id string) (sx.Node, bool) {
	for _, n := range g.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return sx.Node{}, false
}

func (ix *index) children_(id string) []sx.Node {
	var out []sx.Node
	for _, c := range ix.children[id] {
		if n, ok := ix.nodes[c]; ok {
			out = append(out, n)
		}
	}
	return out
}

func (ix *index) subtreeWeight(id string) int {
	seen := map[string]bool{}
	var walk func(string) int
	walk = func(cur string) int {
		if seen[cur] {
			return 0
		}
		seen[cur] = true
		n, ok := ix.nodes[cur]
		if !ok {
			return 0
		}
		total := sx.StructuralNodeWeights[n.Kind]
		for _, c := range ix.children[cur] {
			if child, ok := ix.nodes[c]; ok && sx.IsStructuralNode(child.Kind) {
				total += walk(c)
			}
		}
		return total
	}
	return walk(id)
}

var _ = sc.Complexity
