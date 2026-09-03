package sx

import (
	"encoding/json"
	"io"
	"sort"
)

func Canonical(g *Graph) Graph {
	if g == nil {
		return Graph{}
	}
	out := *g
	out.Nodes = append([]Node(nil), g.Nodes...)
	out.Edges = append([]Edge(nil), g.Edges...)
	out.Roots = append([]string(nil), g.Roots...)
	sort.Slice(out.Nodes, func(i, j int) bool { return out.Nodes[i].ID < out.Nodes[j].ID })
	sort.Slice(out.Edges, func(i, j int) bool { return out.Edges[i].ID < out.Edges[j].ID })
	sort.Strings(out.Roots)
	return out
}

func Encode(w io.Writer, g *Graph) error {
	canon := Canonical(g)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(canon)
}

func MarshalCanonical(g *Graph) ([]byte, error) {
	canon := Canonical(g)
	return json.MarshalIndent(canon, "", "  ")
}
