package sx

import (
	"fmt"
)

func Merge(graphs ...*Graph) (*Graph, error) {
	out := &Graph{
		Metadata: Metadata{
			SchemaVersion:          SchemaVersion,
			ModelVersion:           ModelVersion,
			FrontendID:             "mixed",
			FrontendVersion:        "merged",
			TranslationSpecVersion: "merged",
			SourceUnitID:           "repository",
		},
	}
	seenNodes := map[string]Node{}
	seenRoots := map[string]bool{}
	externalByIdentity := map[string]string{}
	for _, g := range graphs {
		if err := Validate(g); err != nil {
			return nil, err
		}
		remap := map[string]string{}
		for _, n := range g.Nodes {
			id := n.ID
			if n.Kind == "root" && attrString(n, "root_kind") == "external_root" {
				identity := attrString(n, "identity")
				if existing, ok := externalByIdentity[identity]; ok {
					remap[id] = existing
					continue
				}
				externalByIdentity[identity] = id
			}
			if _, ok := seenNodes[id]; ok {
				return nil, fmt.Errorf("duplicate node id while merging: %s", id)
			}
			seenNodes[id] = n
			out.Nodes = append(out.Nodes, n)
			remap[id] = id
		}
		for _, rid := range g.Roots {
			mapped := remap[rid]
			if !seenRoots[mapped] {
				seenRoots[mapped] = true
				out.Roots = append(out.Roots, mapped)
			}
		}
		for i, e := range g.Edges {
			e.From = remap[e.From]
			e.To = remap[e.To]
			e.ID = fmt.Sprintf("edge:%s:%06d:%s", g.Metadata.SourceUnitID, i+1, e.Kind)
			out.Edges = append(out.Edges, e)
		}
	}
	canon := Canonical(out)
	if err := Validate(&canon); err != nil {
		return nil, err
	}
	return &canon, nil
}

func attrString(n Node, key string) string {
	if n.Attributes == nil {
		return ""
	}
	if v, ok := n.Attributes[key].(string); ok {
		return v
	}
	return ""
}
