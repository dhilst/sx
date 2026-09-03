package sx

import (
	"fmt"
)

func Validate(g *Graph) error {
	if g == nil {
		return fmt.Errorf("SC-VAL: graph is nil")
	}
	if g.Metadata.SchemaVersion == "" || g.Metadata.ModelVersion == "" ||
		g.Metadata.FrontendID == "" || g.Metadata.FrontendVersion == "" ||
		g.Metadata.TranslationSpecVersion == "" || g.Metadata.SourceUnitID == "" {
		return fmt.Errorf("SC-VAL-10: required metadata is missing")
	}
	if g.Metadata.ModelVersion != ModelVersion {
		return fmt.Errorf("SC-VAL-3: unsupported model_version %q", g.Metadata.ModelVersion)
	}

	nodes := map[string]Node{}
	for _, n := range g.Nodes {
		if n.ID == "" {
			return fmt.Errorf("SC-VAL: node id is missing")
		}
		if _, exists := nodes[n.ID]; exists {
			return fmt.Errorf("SC-VAL: duplicate node id %q", n.ID)
		}
		if !IsStructuralNode(n.Kind) && !IsStateNode(n.Kind) {
			return fmt.Errorf("SC-VAL-3: unknown node kind %q", n.Kind)
		}
		if !n.Source.Synthetic && !validSource(n.Source) {
			return fmt.Errorf("SC-VAL-1: node %q has no concrete source mapping", n.ID)
		}
		if n.Source.Synthetic && n.Source.SyntheticReason == "" {
			return fmt.Errorf("SC-SOURCE-4: synthetic node %q has no reason", n.ID)
		}
		nodes[n.ID] = n
	}

	rootSeen := map[string]bool{}
	for _, rid := range g.Roots {
		n, ok := nodes[rid]
		if !ok {
			return fmt.Errorf("SC-VAL-9: root %q is missing", rid)
		}
		if n.Kind != "root" {
			return fmt.Errorf("SC-VAL-9: root %q has node kind %q", rid, n.Kind)
		}
		if rootIdentity(n) == "" {
			return fmt.Errorf("SC-VAL-9: root %q identity is missing", rid)
		}
		id := rootIdentity(n)
		if rootSeen[id] {
			return fmt.Errorf("SC-VAL-9: duplicate root identity %q", id)
		}
		rootSeen[id] = true
	}

	containedByRoot := map[string]string{}
	for _, rid := range g.Roots {
		if err := walkContainment(rid, rid, nodes, g.Edges, containedByRoot, map[string]bool{}); err != nil {
			return err
		}
	}

	for _, e := range g.Edges {
		from, ok := nodes[e.From]
		if !ok {
			return fmt.Errorf("SC-VAL: edge %q source node %q is missing", e.ID, e.From)
		}
		to, ok := nodes[e.To]
		if !ok {
			return fmt.Errorf("SC-VAL: edge %q target node %q is missing", e.ID, e.To)
		}
		if _, ok := EdgeWeights[e.Kind]; !ok {
			return fmt.Errorf("SC-VAL-3: unknown edge kind %q", e.Kind)
		}
		if !e.Source.Synthetic && !validSource(e.Source) {
			return fmt.Errorf("SC-VAL-2: edge %q has no concrete source mapping", e.ID)
		}
		if e.Source.Synthetic && e.Source.SyntheticReason == "" {
			return fmt.Errorf("SC-SOURCE-4: synthetic edge %q has no reason", e.ID)
		}
		switch e.Kind {
		case "call":
			if from.Kind != "call" && from.Kind != "defer" && from.Kind != "concurrent_call" {
				return fmt.Errorf("SC-VAL-6: call edge %q starts at %q", e.ID, from.Kind)
			}
			if to.Kind != "root" {
				return fmt.Errorf("SC-VAL-6: call edge %q does not target a root", e.ID)
			}
		case "read", "write":
			if !IsStructuralNode(from.Kind) || !IsStateNode(to.Kind) {
				return fmt.Errorf("SC-VAL-7: %s edge %q must go structural->state", e.Kind, e.ID)
			}
		case "data_dependency", "capture", "state_escape", "alias":
			if !IsStateNode(from.Kind) || !IsStateNode(to.Kind) {
				return fmt.Errorf("SC-VAL-8: %s edge %q must connect state nodes", e.Kind, e.ID)
			}
		case "containment", "control", "exception":
			if !IsStructuralNode(from.Kind) || !IsStructuralNode(to.Kind) {
				return fmt.Errorf("SC-VAL: %s edge %q must connect structural nodes", e.Kind, e.ID)
			}
		}
	}
	return nil
}

func validSource(s Source) bool {
	return s.Path != "" && s.StartLine > 0 && s.StartColumn > 0 && s.EndLine > 0 && s.EndColumn > 0
}

func rootIdentity(n Node) string {
	if n.Attributes == nil {
		return ""
	}
	if v, ok := n.Attributes["identity"].(string); ok {
		return v
	}
	return ""
}

func walkContainment(rootID, id string, nodes map[string]Node, edges []Edge, seen map[string]string, stack map[string]bool) error {
	if stack[id] {
		return fmt.Errorf("SC-VAL-5: containment cycle at %q", id)
	}
	stack[id] = true
	for _, e := range edges {
		if e.Kind != "containment" || e.From != id {
			continue
		}
		to := nodes[e.To]
		if !IsStructuralNode(to.Kind) {
			continue
		}
		if prev, ok := seen[e.To]; ok && prev != rootID {
			return fmt.Errorf("SC-VAL-4: structural node %q is contained by roots %q and %q", e.To, prev, rootID)
		}
		seen[e.To] = rootID
		if err := walkContainment(rootID, e.To, nodes, edges, seen, stack); err != nil {
			return err
		}
	}
	delete(stack, id)
	return nil
}
