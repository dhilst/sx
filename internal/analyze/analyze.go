// Package analyze finds simplification opportunities in an .sx graph and
// quantifies each one with the same cost model the scorer uses.
//
// The graph carries no expression semantics, so an opportunity is a *plan*:
// what to change, where in the source, what sx computes it is worth, and which
// preconditions the graph could verify. Executing a plan and proving it
// preserves behaviour remain the caller's job.
package analyze

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"purgatrix/internal/sc"
	"purgatrix/internal/sx"
)

// Plan is one quantified opportunity.
type Plan struct {
	Kind      string    `json:"kind"`
	Path      string    `json:"path"`
	StartLine int       `json:"start_line"`
	EndLine   int       `json:"end_line"`
	Saving    int       `json:"saving"`
	Exact     int       `json:"exact_saving"`
	Verified  bool      `json:"verified"`
	Detail    string    `json:"detail"`
	NodeID    string    `json:"node_id"`
	Anchor    string    `json:"anchor"`
	Blockers  []string  `json:"blockers,omitempty"`
	Source    sx.Source `json:"source"`

	// group carries the duplicate occurrences so the rewrite can fold them.
	// It is internal: the JSON contract stays a description of the plan.
	group []subtreeSignature
}

// Best is the measured saving when the rewrite could be scored, and the
// estimate otherwise.
func (p Plan) Best() int {
	if p.Verified {
		return p.Exact
	}
	return p.Saving
}

// Report is the analyzer's output, ordered by what it is worth.
type Report struct {
	Total     int    `json:"total"`
	Plans     []Plan `json:"plans"`
	Selected  []Plan `json:"selected"`
	Selection int    `json:"selection_saving"`
}

// An else arm is nested under a branch and a case, both of which increase
// structural depth.
const depthLevelsRemoved = 2

type analyzer struct {
	g        *sx.Graph
	h        sx.Hyperparameters
	nodes    map[string]sx.Node
	out      map[string][]sx.Edge
	in       map[string][]sx.Edge
	children map[string][]string
	parent   map[string]string
	sigCache map[string]subtreeSignature
	source   func(path string) ([]string, bool)
}

// verifyLimit caps how many plans are priced by rewriting and rescoring.
// Each measurement clones, canonicalises, validates and rescores the whole
// graph, which is around a second on a repository-sized one, so pricing every
// plan costs a minute for a list nobody reads past the top. Plans beyond the
// limit keep their estimate and say so.
const verifyLimit = 12

// Analyze runs every pass over the graph.
func Analyze(g *sx.Graph) (Report, error) {
	return AnalyzeWithLimit(g, verifyLimit)
}

// AnalyzeWithSource is Analyze with the source tree available, which lets the
// duplicate pass confirm a match against the actual bytes instead of resting on
// structure alone.
func AnalyzeWithSource(g *sx.Graph, root string) (Report, error) {
	return analyzeWith(g, verifyLimit, sourceReader(root))
}

// AnalyzeWithLimit is Analyze with control over how many plans get measured.
func AnalyzeWithLimit(g *sx.Graph, limit int) (Report, error) {
	return analyzeWith(g, limit, nil)
}

// sourceReader reads and caches files under root, so a pass can look at the
// text a span covers.
func sourceReader(root string) func(string) ([]string, bool) {
	cache := map[string][]string{}
	missing := map[string]bool{}
	return func(path string) ([]string, bool) {
		if lines, ok := cache[path]; ok {
			return lines, true
		}
		if missing[path] {
			return nil, false
		}
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			missing[path] = true
			return nil, false
		}
		lines := strings.Split(string(data), "\n")
		cache[path] = lines
		return lines, true
	}
}

func analyzeWith(g *sx.Graph, limit int, source func(string) ([]string, bool)) (Report, error) {
	if err := sx.Validate(g); err != nil {
		return Report{}, err
	}
	a := &analyzer{
		g:        g,
		h:        sx.DefaultHyperparameters(),
		nodes:    map[string]sx.Node{},
		out:      map[string][]sx.Edge{},
		in:       map[string][]sx.Edge{},
		children: map[string][]string{},
		parent:   map[string]string{},
		sigCache: map[string]subtreeSignature{},
		source:   source,
	}
	for _, n := range g.Nodes {
		a.nodes[n.ID] = n
	}
	for _, e := range g.Edges {
		a.out[e.From] = append(a.out[e.From], e)
		a.in[e.To] = append(a.in[e.To], e)
		if e.Kind == "containment" {
			a.children[e.From] = append(a.children[e.From], e.To)
			a.parent[e.To] = e.From
		}
	}

	var plans []Plan
	plans = append(plans, a.deadState()...)
	plans = append(plans, a.guardInversion()...)
	plans = append(plans, a.excessArity()...)
	plans = append(plans, a.duplicateStructure()...)
	sort.SliceStable(plans, func(i, j int) bool {
		if plans[i].Saving != plans[j].Saving {
			return plans[i].Saving > plans[j].Saving
		}
		if plans[i].Path != plans[j].Path {
			return plans[i].Path < plans[j].Path
		}
		return plans[i].StartLine < plans[j].StartLine
	})

	// Score each rewrite instead of trusting the weight arithmetic. The
	// estimate models the depth term; only the scorer knows the whole charge.
	if before, err := Verify(g); err == nil {
		for i := range plans {
			if limit >= 0 && i >= limit {
				break
			}
			if exact, err := exactSavingFrom(g, before, plans[i]); err == nil {
				plans[i].Exact = exact
				plans[i].Verified = true
			}
		}
	}
	sort.SliceStable(plans, func(i, j int) bool {
		a, b := plans[i].Best(), plans[j].Best()
		if a != b {
			return a > b
		}
		if plans[i].Path != plans[j].Path {
			return plans[i].Path < plans[j].Path
		}
		return plans[i].StartLine < plans[j].StartLine
	})

	report := Report{Plans: plans}
	for _, p := range plans {
		report.Total += p.Best()
	}
	report.Selected = selectNonConflicting(plans)
	for _, p := range report.Selected {
		report.Selection += p.Best()
	}
	return report, nil
}

// deadState finds state that is written and never read. The saving is the
// state node's own cost plus the cost of every edge that disappears with it.
func (a *analyzer) deadState() []Plan {
	var plans []Plan
	for _, n := range a.g.Nodes {
		if !sx.IsStateNode(n.Kind) || n.Kind == "global" || n.Kind == "external_state" {
			continue
		}
		if n.Source.Synthetic {
			continue
		}
		writes, reads, deps, indirect := 0, 0, 0, 0
		saving := sx.StateNodeWeights[n.Kind]
		for _, e := range a.in[n.ID] {
			switch e.Kind {
			case "write":
				// A write through a selector or an index mutates something the
				// variable refers to. The variable is live and the write is the
				// point of the statement, so this is not dead state.
				if s, _ := e.Attributes["target"].(string); s == "indirect" {
					indirect++
				}
				writes++
				saving += sx.EdgeWeights[e.Kind]
			case "read":
				reads++
			case "data_dependency", "capture", "state_escape", "alias":
				deps++
			}
		}
		for _, e := range a.out[n.ID] {
			if e.Kind == "data_dependency" || e.Kind == "capture" || e.Kind == "state_escape" || e.Kind == "alias" {
				deps++
			}
		}
		if writes == 0 || reads > 0 || deps > 0 || indirect > 0 {
			continue
		}
		// Only a local is safely deletable in place. A parameter, receiver or
		// result belongs to a signature, and a name the frontend saw assigned
		// but never declared may well be a package-level variable read from
		// another root entirely.
		if n.Kind != "local" {
			continue
		}
		// A name this root never declared is declared somewhere else, so the
		// reads that would contradict "never read" are outside this root.
		if declared, ok := n.Attributes["declared_in_root"].(bool); ok && !declared {
			continue
		}
		var blockers []string
		plans = append(plans, Plan{
			Kind:      "dead_state",
			Path:      n.Source.Path,
			StartLine: n.Source.StartLine,
			EndLine:   n.Source.EndLine,
			Saving:    saving,
			Detail:    stateName(n) + " is written " + plural(writes, "time") + " and never read",
			NodeID:    n.ID,
			Anchor:    a.rootOf(n.ID),
			Blockers:  blockers,
			Source:    n.Source,
		})
	}
	return plans
}

// guardInversion finds a branch whose light arm is a lone terminal. Inverting
// it lifts the heavy arm out of the else entirely.
//
// Both `branch` and `case` increase structural depth, so an else arm sits two
// depth-increasing nodes below its parent. Lifting it therefore removes two
// levels from every node beneath it, and cost is weight x (1 + depth), so the
// saving is twice the arm's node weight plus the else `case` node that goes
// away with it.
//
// This is an estimate, not an identity. It models the depth term exactly and
// ignores two smaller effects: dropping an else makes the outer branch
// else-less, so the frontend synthesises a replacement case, and the parent
// gains a child, which costs structural breadth. Measured against
// path/filepath/symlink.go the estimate came to 67 where the scorer charged
// 63. An exact figure needs the rewrite applied to the graph and the graph
// rescored, which is the natural next step for this package.
func (a *analyzer) guardInversion() []Plan {
	var plans []Plan
	for _, n := range a.g.Nodes {
		if n.Kind != "branch" || n.Source.Synthetic {
			continue
		}
		var cases []sx.Node
		for _, c := range a.children[n.ID] {
			if a.nodes[c].Kind == "case" {
				cases = append(cases, a.nodes[c])
			}
		}
		if len(cases) != 2 {
			continue
		}
		// A synthetic arm has no text of its own; it borrows the enclosing
		// span, so it cannot be used as a rewrite handle.
		synthetic := false
		for _, c := range cases {
			if c.Source.Synthetic {
				synthetic = true
			}
		}
		if synthetic {
			continue
		}
		w0, w1 := a.subtreeWeight(cases[0].ID), a.subtreeWeight(cases[1].ID)
		light := cases[0]
		lw, hw := w0, w1
		if w1 < w0 {
			light = cases[1]
			lw, hw = w1, w0
		}
		if hw < 8 || lw > 2 || !a.isLoneTerminal(light.ID) {
			continue
		}
		var blockers []string
		if a.containsCall(light.ID) || a.conditionHasCall(n.ID) {
			blockers = append(blockers, "the condition or the lifted arm calls out, so order of effects can change")
		}
		plans = append(plans, Plan{
			Kind:      "guard_inversion",
			Path:      n.Source.Path,
			StartLine: n.Source.StartLine,
			EndLine:   n.Source.EndLine,
			Saving:    depthLevelsRemoved*hw + sx.StructuralNodeWeights["case"],
			Detail:    "invert the condition and return early; the other arm leaves the else",
			NodeID:    n.ID,
			Anchor:    n.ID,
			Blockers:  blockers,
			Source:    n.Source,
		})
	}
	return plans
}

// excessArity reports roots paying the arity penalty.
func (a *analyzer) excessArity() []Plan {
	var plans []Plan
	for _, rid := range a.g.Roots {
		n := a.nodes[rid]
		if attrString(n, "root_kind") == "external_root" {
			continue
		}
		params := attrInt(n, "parameter_count")
		results := attrInt(n, "result_count")
		excess := max(0, params-a.h.ExcessArityThreshold) + max(0, results-a.h.ExcessArityThreshold)
		if excess == 0 {
			continue
		}
		plans = append(plans, Plan{
			Kind:      "excess_arity",
			Path:      n.Source.Path,
			StartLine: n.Source.StartLine,
			EndLine:   n.Source.EndLine,
			Saving:    excess * a.h.ExcessArityWeight,
			Detail:    attrString(n, "identity") + " carries " + plural(params, "parameter") + " and " + plural(results, "result"),
			NodeID:    rid,
			Anchor:    rid,
			Blockers: []string{
				"this is a cost attribution, not a transformation: the parameters have to go somewhere, and a struct to hold them costs at least as much (measured twice against this codebase)",
			},
			Source: n.Source,
		})
	}
	return plans
}

// selectNonConflicting picks the best set of plans whose source ranges do not
// overlap. Plans anchor to nested regions, so this is the classic weighted
// interval selection: sort by end line, then keep a plan when it starts after
// the last kept one ends.
func selectNonConflicting(plans []Plan) []Plan {
	byFile := map[string][]Plan{}
	for _, p := range plans {
		if len(p.Blockers) > 0 {
			continue
		}
		byFile[p.Path] = append(byFile[p.Path], p)
	}
	var files []string
	for f := range byFile {
		files = append(files, f)
	}
	sort.Strings(files)

	var out []Plan
	for _, f := range files {
		group := byFile[f]
		sort.SliceStable(group, func(i, j int) bool {
			if group[i].EndLine != group[j].EndLine {
				return group[i].EndLine < group[j].EndLine
			}
			return group[i].Saving > group[j].Saving
		})
		lastEnd := -1
		for _, p := range group {
			if p.StartLine > lastEnd {
				out = append(out, p)
				lastEnd = p.EndLine
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Saving > out[j].Saving })
	return out
}

func (a *analyzer) subtreeWeight(id string) int {
	seen := map[string]bool{}
	var walk func(string) int
	walk = func(cur string) int {
		if seen[cur] {
			return 0
		}
		seen[cur] = true
		total := sx.StructuralNodeWeights[a.nodes[cur].Kind]
		for _, c := range a.children[cur] {
			if sx.IsStructuralNode(a.nodes[c].Kind) {
				total += walk(c)
			}
		}
		return total
	}
	return walk(id)
}

func (a *analyzer) isLoneTerminal(caseID string) bool {
	kids := a.children[caseID]
	if len(kids) != 1 {
		return false
	}
	switch a.nodes[kids[0]].Kind {
	case "return", "jump":
		return true
	}
	return false
}

func (a *analyzer) containsCall(id string) bool {
	for _, c := range a.children[id] {
		switch a.nodes[c].Kind {
		case "call", "defer", "concurrent_call":
			return true
		}
		if a.containsCall(c) {
			return true
		}
	}
	return false
}

// conditionHasCall reports whether the branch itself calls out, which the graph
// records as a call edge leaving the branch node.
func (a *analyzer) conditionHasCall(branchID string) bool {
	for _, e := range a.out[branchID] {
		if e.Kind == "call" {
			return true
		}
	}
	return false
}

func (a *analyzer) rootOf(id string) string {
	n := a.nodes[id]
	if n.RootID != "" {
		return n.RootID
	}
	for cur := id; ; {
		p, ok := a.parent[cur]
		if !ok {
			return cur
		}
		if a.nodes[p].Kind == "root" {
			return p
		}
		cur = p
	}
}

func stateName(n sx.Node) string {
	if s, ok := n.Attributes["name"].(string); ok && s != "" {
		return s
	}
	return n.Kind
}

func attrString(n sx.Node, key string) string {
	s, _ := n.Attributes[key].(string)
	return s
}

func attrInt(n sx.Node, key string) int {
	switch v := n.Attributes[key].(type) {
	case int:
		return v
	case float64:
		return int(v)
	}
	return 0
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return itoa(n) + " " + word + "s"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

// Verify recomputes the score so a caller can compare a predicted saving with
// what the scorer actually charges after a rewrite.
func Verify(g *sx.Graph) (int, error) {
	r, err := sc.Complexity(g)
	if err != nil {
		return 0, err
	}
	return r.Total, nil
}
