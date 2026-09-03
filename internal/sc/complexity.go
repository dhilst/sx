package sc

import (
	"fmt"
	"sort"

	"purgatrix/internal/sx"
)

func Complexity(g *sx.Graph) (Report, error) {
	if err := sx.Validate(g); err != nil {
		return Report{}, err
	}

	c := calculator{
		g:      g,
		h:      sx.DefaultHyperparameters(),
		nodes:  map[string]sx.Node{},
		out:    map[string][]sx.Edge{},
		in:     map[string][]sx.Edge{},
		depths: map[string]int{},
		stateD: map[string]int{},
	}
	for _, n := range g.Nodes {
		c.nodes[n.ID] = n
	}
	for _, e := range g.Edges {
		c.out[e.From] = append(c.out[e.From], e)
		c.in[e.To] = append(c.in[e.To], e)
	}
	c.computeStateDepths()

	var report Report
	report.ModelVersion = sx.ModelVersion
	files := map[string]*FileReport{}
	for _, rid := range append([]string(nil), g.Roots...) {
		root := c.nodes[rid]
		if attrString(root, "root_kind") == "external_root" {
			continue
		}
		rr := c.scoreRoot(rid)
		report.Roots = append(report.Roots, rr)
		report.Structure += rr.Structure
		report.State += rr.State
		report.Total += rr.Total
		fr := files[rr.File]
		if fr == nil {
			fr = &FileReport{Path: rr.File}
			files[rr.File] = fr
		}
		fr.Structure += rr.Structure
		fr.State += rr.State
		fr.Total += rr.Total
	}
	report.StructuralCycles = c.structuralCycleCost()
	report.StateCycles = c.stateCycleCost()
	report.Total += report.StructuralCycles + report.StateCycles

	for _, fr := range files {
		report.Files = append(report.Files, *fr)
	}
	sort.Slice(report.Roots, func(i, j int) bool {
		return report.Roots[i].Identity < report.Roots[j].Identity
	})
	sort.Slice(report.Files, func(i, j int) bool { return report.Files[i].Path < report.Files[j].Path })
	sort.Slice(c.hotspots, func(i, j int) bool {
		a, b := c.hotspots[i], c.hotspots[j]
		if a.Contribution != b.Contribution {
			return a.Contribution > b.Contribution
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.StartLine != b.StartLine {
			return a.StartLine < b.StartLine
		}
		if a.StartColumn != b.StartColumn {
			return a.StartColumn < b.StartColumn
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.ID < b.ID
	})
	if len(c.hotspots) > 20 {
		report.Hotspots = c.hotspots[:20]
	} else {
		report.Hotspots = c.hotspots
	}
	return report, nil
}

type calculator struct {
	g        *sx.Graph
	h        sx.Hyperparameters
	nodes    map[string]sx.Node
	out      map[string][]sx.Edge
	in       map[string][]sx.Edge
	depths   map[string]int
	stateD   map[string]int
	hotspots []Hotspot
}

func (c *calculator) scoreRoot(rid string) RootReport {
	root := c.nodes[rid]
	structural := c.structuralSlice(rid)
	c.computeStructuralDepths(rid, structural)
	states, stateEdges := c.stateSlice(rid, structural)

	structure := 0
	state := 0
	nodeContribution := map[string]int{}
	stateContribution := map[string]int{}

	for id := range structural {
		n := c.nodes[id]
		base := sx.StructuralNodeWeights[n.Kind]
		cost := base * (1 + c.h.StructuralDepthWeight*c.depths[id])
		breadth := c.h.StructuralBreadthWeight * c.structuralBreadth(n)
		structure += cost + breadth
		nodeContribution[id] += cost + breadth
	}
	for id := range structural {
		for _, e := range c.out[id] {
			if e.Kind != "control" && e.Kind != "exception" && e.Kind != "call" {
				continue
			}
			cost := sx.EdgeWeights[e.Kind]
			if e.Kind == "call" {
				switch attrStringEdge(e, "resolution") {
				case "external":
					cost += c.h.ExternalCallWeight
				case "unresolved":
					cost += c.h.UnresolvedCallWeight
				}
			}
			structure += cost
			nodeContribution[id] += cost
		}
	}
	arity := c.arityCost(root)
	structure += arity
	nodeContribution[rid] += arity

	for sid := range states {
		s := c.nodes[sid]
		cost := sx.StateNodeWeights[s.Kind] + c.h.StateDepthWeight*c.stateD[sid]
		depBreadth := c.h.DependencyBreadthWeight * max(0, c.dependencyBreadth(sid)-1)
		aliasBreadth := c.h.AliasBreadthWeight * c.aliasBreadth(sid)
		state += cost + depBreadth + aliasBreadth
		stateContribution[sid] += cost + depBreadth + aliasBreadth
	}
	for id := range structural {
		stb := c.h.StateBreadthWeight * c.stateBreadth(id)
		state += stb
		nodeContribution[id] += stb
	}
	for _, e := range stateEdges {
		cost := sx.EdgeWeights[e.Kind]
		state += cost
		nodeContribution[e.From] += cost
		stateContribution[e.To] += cost
	}

	for id, v := range nodeContribution {
		if v > 0 {
			c.addHotspot(c.nodes[id], v)
		}
	}
	for id, v := range stateContribution {
		if v > 0 {
			c.addHotspot(c.nodes[id], v)
		}
	}

	return RootReport{
		ID:        rid,
		Identity:  attrString(root, "identity"),
		Kind:      attrString(root, "root_kind"),
		File:      root.Source.Path,
		Structure: structure,
		State:     state,
		Total:     structure + state,
	}
}

func (c *calculator) structuralSlice(rid string) map[string]bool {
	seen := map[string]bool{rid: true}
	var walk func(string)
	walk = func(id string) {
		for _, e := range c.out[id] {
			if e.Kind != "containment" {
				continue
			}
			n := c.nodes[e.To]
			if !sx.IsStructuralNode(n.Kind) || seen[e.To] {
				continue
			}
			seen[e.To] = true
			walk(e.To)
		}
	}
	walk(rid)
	return seen
}

func (c *calculator) computeStructuralDepths(rid string, structural map[string]bool) {
	var walk func(string, int)
	walk = func(id string, depth int) {
		c.depths[id] = depth
		nextDepth := depth
		if isDepthIncreasing(c.nodes[id].Kind) {
			nextDepth++
		}
		for _, e := range c.out[id] {
			if e.Kind == "containment" && structural[e.To] {
				walk(e.To, nextDepth)
			}
		}
	}
	walk(rid, 0)
}

func isDepthIncreasing(kind string) bool {
	switch kind {
	case "branch", "multi_branch", "case", "loop", "defer", "concurrent_call", "closure_literal", "panic", "recover":
		return true
	default:
		return false
	}
}

func (c *calculator) structuralBreadth(n sx.Node) int {
	count := 0
	for _, e := range c.out[n.ID] {
		if e.Kind != "containment" {
			continue
		}
		child := c.nodes[e.To]
		if !sx.IsStructuralNode(child.Kind) {
			continue
		}
		if n.Kind == "branch" || n.Kind == "multi_branch" {
			if child.Kind == "case" {
				count++
			}
			continue
		}
		count++
	}
	if n.Kind == "branch" || n.Kind == "multi_branch" {
		return count
	}
	return max(0, count-1)
}

func (c *calculator) stateSlice(rid string, structural map[string]bool) (map[string]bool, []sx.Edge) {
	states := map[string]bool{}
	for _, n := range c.g.Nodes {
		if sx.IsStateNode(n.Kind) && (n.RootID == rid || attrString(n, "owner_root_id") == rid) {
			states[n.ID] = true
		}
	}
	for id := range structural {
		for _, e := range c.out[id] {
			if e.Kind == "read" || e.Kind == "write" {
				states[e.To] = true
			}
		}
	}
	var edges []sx.Edge
	for _, e := range c.g.Edges {
		switch e.Kind {
		case "read", "write":
			if structural[e.From] {
				edges = append(edges, e)
			}
		case "data_dependency", "capture", "state_escape", "alias":
			fromOwned := c.nodes[e.From].RootID == rid || attrString(c.nodes[e.From], "owner_root_id") == rid
			toOwned := c.nodes[e.To].RootID == rid || attrString(c.nodes[e.To], "owner_root_id") == rid
			if fromOwned || toOwned || (states[e.From] && states[e.To]) {
				states[e.From] = true
				states[e.To] = true
				edges = append(edges, e)
			}
		}
	}
	return states, edges
}

func (c *calculator) stateBreadth(id string) int {
	refs := map[string]bool{}
	for _, e := range c.out[id] {
		if e.Kind == "read" || e.Kind == "write" {
			refs[e.To] = true
		}
	}
	return max(0, len(refs)-1)
}

func (c *calculator) dependencyBreadth(id string) int {
	count := 0
	for _, e := range c.in[id] {
		if e.Kind == "data_dependency" {
			count++
		}
	}
	return count
}

func (c *calculator) aliasBreadth(id string) int {
	count := 0
	for _, e := range c.in[id] {
		if e.Kind == "alias" {
			count++
		}
	}
	for _, e := range c.out[id] {
		if e.Kind == "alias" {
			count++
		}
	}
	return count
}

func (c *calculator) arityCost(root sx.Node) int {
	params := attrInt(root, "parameter_count")
	receivers := attrInt(root, "receiver_count")
	results := attrInt(root, "result_count")
	arity := params + receivers + results
	return c.h.ArityParameterWeight*(params+receivers) +
		c.h.ArityResultWeight*results +
		c.h.ExcessArityWeight*max(0, arity-c.h.ExcessArityThreshold)
}

func (c *calculator) addHotspot(n sx.Node, contribution int) {
	c.hotspots = append(c.hotspots, Hotspot{
		ID:           n.ID,
		Kind:         n.Kind,
		Path:         n.Source.Path,
		StartLine:    n.Source.StartLine,
		StartColumn:  n.Source.StartColumn,
		Contribution: contribution,
		RootID:       n.RootID,
	})
}

func attrString(n sx.Node, key string) string {
	if n.Attributes == nil {
		return ""
	}
	if v, ok := n.Attributes[key].(string); ok {
		return v
	}
	return ""
}

func attrStringEdge(e sx.Edge, key string) string {
	if e.Attributes == nil {
		return ""
	}
	if v, ok := e.Attributes[key].(string); ok {
		return v
	}
	return ""
}

func attrInt(n sx.Node, key string) int {
	if n.Attributes == nil {
		return 0
	}
	switch v := n.Attributes[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (c *calculator) computeStateDepths() {
	nodes := []string{}
	for _, n := range c.g.Nodes {
		if sx.IsStateNode(n.Kind) {
			nodes = append(nodes, n.ID)
		}
	}
	graph := map[string][]string{}
	for _, e := range c.g.Edges {
		if !isStateDependencyEdge(e.Kind) {
			continue
		}
		graph[e.From] = append(graph[e.From], e.To)
		if e.Kind == "alias" {
			graph[e.To] = append(graph[e.To], e.From)
		}
	}
	sccs := stronglyConnected(nodes, graph)
	compOf := map[string]int{}
	for i, comp := range sccs {
		for _, id := range comp {
			compOf[id] = i
		}
	}
	dag := map[int]map[int]bool{}
	incoming := map[int]int{}
	for from, tos := range graph {
		for _, to := range tos {
			a, b := compOf[from], compOf[to]
			if a == b {
				continue
			}
			if dag[a] == nil {
				dag[a] = map[int]bool{}
			}
			if !dag[a][b] {
				dag[a][b] = true
				incoming[b]++
			}
		}
	}
	memo := map[int]int{}
	var depth func(int) int
	depth = func(comp int) int {
		if v, ok := memo[comp]; ok {
			return v
		}
		best := 0
		for pred, tos := range dag {
			if tos[comp] {
				best = max(best, depth(pred)+1)
			}
		}
		memo[comp] = best
		return best
	}
	_ = incoming
	for id, comp := range compOf {
		c.stateD[id] = depth(comp)
	}
}

func isStateDependencyEdge(kind string) bool {
	switch kind {
	case "data_dependency", "capture", "state_escape", "alias":
		return true
	default:
		return false
	}
}

func (c *calculator) structuralCycleCost() int {
	roots := append([]string(nil), c.g.Roots...)
	graph := map[string][]string{}
	for _, e := range c.g.Edges {
		if e.Kind == "call" {
			if containsRoot(roots, e.From) {
				continue
			}
			target := c.nodes[e.To]
			if target.Kind == "root" {
				sourceRoot := c.nodes[e.From].RootID
				if sourceRoot != "" {
					graph[sourceRoot] = append(graph[sourceRoot], e.To)
				}
			}
		}
	}
	return c.cycleCost(roots, graph, c.h.StructuralCycleWeight)
}

func (c *calculator) stateCycleCost() int {
	nodes := []string{}
	for _, n := range c.g.Nodes {
		if sx.IsStateNode(n.Kind) {
			nodes = append(nodes, n.ID)
		}
	}
	graph := map[string][]string{}
	for _, e := range c.g.Edges {
		if !isStateDependencyEdge(e.Kind) {
			continue
		}
		graph[e.From] = append(graph[e.From], e.To)
		if e.Kind == "alias" {
			graph[e.To] = append(graph[e.To], e.From)
		}
	}
	return c.cycleCost(nodes, graph, c.h.StateCycleWeight)
}

func (c *calculator) cycleCost(nodes []string, graph map[string][]string, weight int) int {
	total := 0
	for _, comp := range stronglyConnected(nodes, graph) {
		internal := 0
		set := map[string]bool{}
		for _, id := range comp {
			set[id] = true
		}
		for _, id := range comp {
			for _, to := range graph[id] {
				if set[to] {
					internal++
				}
			}
		}
		if len(comp) > 1 || internal > 0 {
			total += weight * (len(comp) + internal - 1)
		}
	}
	return total
}

func stronglyConnected(nodes []string, graph map[string][]string) [][]string {
	sort.Strings(nodes)
	index := 0
	stack := []string{}
	onStack := map[string]bool{}
	indices := map[string]int{}
	low := map[string]int{}
	var comps [][]string
	var visit func(string)
	visit = func(v string) {
		indices[v] = index
		low[v] = index
		index++
		stack = append(stack, v)
		onStack[v] = true
		tos := append([]string(nil), graph[v]...)
		sort.Strings(tos)
		for _, w := range tos {
			if _, ok := indices[w]; !ok {
				visit(w)
				low[v] = min(low[v], low[w])
			} else if onStack[w] {
				low[v] = min(low[v], indices[w])
			}
		}
		if low[v] == indices[v] {
			var comp []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp = append(comp, w)
				if w == v {
					break
				}
			}
			sort.Strings(comp)
			comps = append(comps, comp)
		}
	}
	for _, n := range nodes {
		if _, ok := indices[n]; !ok {
			visit(n)
		}
	}
	return comps
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func containsRoot(roots []string, id string) bool {
	for _, r := range roots {
		if r == id {
			return true
		}
	}
	return false
}

func FormatText(r Report) string {
	out := fmt.Sprintf("Structure: %d\nState: %d\nStructural cycles: %d\nState cycles: %d\nTotal: %d\n",
		r.Structure, r.State, r.StructuralCycles, r.StateCycles, r.Total)
	out += "\nRoot                         Structure   State   Total\n"
	out += "------------------------------------------------------\n"
	for _, root := range r.Roots {
		name := root.Identity
		if len(name) > 28 {
			name = name[len(name)-28:]
		}
		out += fmt.Sprintf("%-28s %9d %7d %7d\n", name, root.Structure, root.State, root.Total)
	}
	return out
}
