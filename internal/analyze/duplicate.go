package analyze

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"purgatrix/internal/sx"
)

// Duplicate structure is found by Merkle-hashing the containment tree: a
// subtree's hash covers its node kind, the ordered hashes of its children, the
// identity of whatever it calls, and how many values it reads and writes.
//
// Shape alone would be far too weak. `if a { return 1 } else { return 2 }` and
// `if b { return 3 } else { return 4 }` are the same shape and different code,
// and the graph holds no expressions to tell them apart. Folding the call
// targets into the hash is what makes a match worth reporting: two subtrees of
// the same shape that call the same functions in the same order are usually the
// same code. They are still only *candidates* - the plan says so.
const (
	minDuplicateWeight = 8
	minDuplicateCount  = 2
)

type subtreeSignature struct {
	hash   string
	weight int
	nodes  []string
}

func (a *analyzer) duplicateStructure() []Plan {
	sigs := map[string][]subtreeSignature{}
	for _, n := range a.g.Nodes {
		if !sx.IsStructuralNode(n.Kind) || n.Kind == "root" || n.Source.Synthetic {
			continue
		}
		sig := a.signature(n.ID)
		if sig.weight < minDuplicateWeight {
			continue
		}
		sigs[sig.hash] = append(sigs[sig.hash], sig)
	}

	var plans []Plan
	for hash, group := range sigs {
		if len(group) < minDuplicateCount {
			continue
		}
		// Keep only the outermost occurrences: a duplicated block also has
		// duplicated children, and reporting every level buries the finding.
		group = a.outermost(group)
		if len(group) < minDuplicateCount {
			continue
		}
		sort.Slice(group, func(i, j int) bool { return group[i].nodes[0] < group[j].nodes[0] })
		anchor := a.nodes[group[0].nodes[0]]
		var sites []string
		for _, s := range group {
			n := a.nodes[s.nodes[0]]
			sites = append(sites, fmt.Sprintf("%s:%d", n.Source.Path, n.Source.StartLine))
		}
		plans = append(plans, Plan{
			Kind:      "duplicate_structure",
			Path:      anchor.Source.Path,
			StartLine: anchor.Source.StartLine,
			EndLine:   anchor.Source.EndLine,
			Saving:    (len(group) - 1) * group[0].weight,
			Detail: fmt.Sprintf("%d occurrences of the same %d-weight structure calling the same targets: %s",
				len(group), group[0].weight, joinLimit(sites, 4)),
			NodeID: group[0].nodes[0],
			Anchor: hash,
			Blockers: []string{
				"the graph holds no expressions, so identical structure is a strong hint and not proof the code is the same",
			},
			Source: anchor.Source,
			group:  group,
		})
	}
	sort.SliceStable(plans, func(i, j int) bool { return plans[i].Saving > plans[j].Saving })
	if len(plans) > 25 {
		plans = plans[:25]
	}
	return plans
}

// signature Merkle-hashes a subtree. Results are memoised: a subtree's hash is
// asked for once per ancestor, so recomputing it turns a linear pass into a
// quadratic one on deep trees.
func (a *analyzer) signature(id string) subtreeSignature {
	if sig, ok := a.sigCache[id]; ok {
		return sig
	}
	n := a.nodes[id]
	h := sha256.New()
	fmt.Fprintf(h, "kind=%s;", n.Kind)

	// What this node calls is the strongest available discriminator.
	var targets []string
	for _, e := range a.out[id] {
		if e.Kind == "call" {
			targets = append(targets, attrString(a.nodes[e.To], "identity")+"/"+attrStringEdge(e, "resolution"))
		}
	}
	sort.Strings(targets)
	for _, t := range targets {
		fmt.Fprintf(h, "calls=%s;", t)
	}

	reads, writes := 0, 0
	for _, e := range a.out[id] {
		switch e.Kind {
		case "read":
			reads++
		case "write":
			writes++
		}
	}
	fmt.Fprintf(h, "reads=%d;writes=%d;", reads, writes)

	weight := sx.StructuralNodeWeights[n.Kind]
	nodes := []string{id}
	children := append([]string(nil), a.children[id]...)
	sort.SliceStable(children, func(i, j int) bool {
		x, y := a.nodes[children[i]].Source, a.nodes[children[j]].Source
		if x.StartLine != y.StartLine {
			return x.StartLine < y.StartLine
		}
		return x.StartColumn < y.StartColumn
	})
	for _, c := range children {
		if !sx.IsStructuralNode(a.nodes[c].Kind) {
			continue
		}
		cs := a.signature(c)
		fmt.Fprintf(h, "child=%s;", cs.hash)
		weight += cs.weight
		nodes = append(nodes, cs.nodes...)
	}
	sig := subtreeSignature{hash: hex.EncodeToString(h.Sum(nil))[:16], weight: weight, nodes: nodes}
	a.sigCache[id] = sig
	return sig
}

// outermost drops occurrences contained by another occurrence in the group.
func (a *analyzer) outermost(group []subtreeSignature) []subtreeSignature {
	inGroup := map[string]bool{}
	for _, s := range group {
		inGroup[s.nodes[0]] = true
	}
	var out []subtreeSignature
	for _, s := range group {
		nested := false
		for cur := s.nodes[0]; ; {
			p, ok := a.parent[cur]
			if !ok {
				break
			}
			if inGroup[p] {
				nested = true
				break
			}
			cur = p
		}
		if !nested {
			out = append(out, s)
		}
	}
	return out
}

func joinLimit(items []string, limit int) string {
	if len(items) > limit {
		items = append(items[:limit:limit], "...")
	}
	out := ""
	for i, s := range items {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

func attrStringEdge(e sx.Edge, key string) string {
	s, _ := e.Attributes[key].(string)
	return s
}
