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
	if _, ok := findNode(g, nodeID); !ok {
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
	branch, ok := findNode(g, branchID)
	if !ok || branch.Kind != "branch" {
		return nil, fmt.Errorf("node %q is not a branch", branchID)
	}
	children := containedChildren(g, branchID)
	var cases []sx.Node
	for _, c := range children {
		if c.Kind == "case" {
			cases = append(cases, c)
		}
	}
	if len(cases) != 2 {
		return nil, fmt.Errorf("branch %q does not have exactly two cases", branchID)
	}
	weights := [2]int{subtreeStructuralWeight(g, cases[0].ID), subtreeStructuralWeight(g, cases[1].ID)}
	heavy := cases[0]
	if weights[1] > weights[0] {
		heavy = cases[1]
	}
	parent, ok := containerOf(g, branchID)
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
	root, ok := findNode(g, rootID)
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

func findNode(g *sx.Graph, id string) (sx.Node, bool) {
	for _, n := range g.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return sx.Node{}, false
}

func containedChildren(g *sx.Graph, id string) []sx.Node {
	var out []sx.Node
	for _, e := range g.Edges {
		if e.Kind == "containment" && e.From == id {
			if n, ok := findNode(g, e.To); ok {
				out = append(out, n)
			}
		}
	}
	return out
}

func containerOf(g *sx.Graph, id string) (string, bool) {
	for _, e := range g.Edges {
		if e.Kind == "containment" && e.To == id {
			return e.From, true
		}
	}
	return "", false
}

func subtreeStructuralWeight(g *sx.Graph, id string) int {
	seen := map[string]bool{}
	var walk func(string) int
	walk = func(cur string) int {
		if seen[cur] {
			return 0
		}
		seen[cur] = true
		n, ok := findNode(g, cur)
		if !ok {
			return 0
		}
		total := sx.StructuralNodeWeights[n.Kind]
		for _, c := range containedChildren(g, cur) {
			if sx.IsStructuralNode(c.Kind) {
				total += walk(c.ID)
			}
		}
		return total
	}
	return walk(id)
}

var _ = sc.Complexity
