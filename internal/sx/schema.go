package sx

const (
	ModelVersion  = "sc-v0"
	SchemaVersion = "sx-v0"
)

type Metadata struct {
	SchemaVersion          string `json:"schema_version"`
	ModelVersion           string `json:"model_version"`
	FrontendID             string `json:"frontend_id"`
	FrontendVersion        string `json:"frontend_version"`
	TranslationSpecVersion string `json:"translation_spec_version"`
	SourceUnitID           string `json:"source_unit_id"`
}

type Source struct {
	Path            string `json:"path"`
	StartLine       int    `json:"start_line"`
	StartColumn     int    `json:"start_column"`
	EndLine         int    `json:"end_line"`
	EndColumn       int    `json:"end_column"`
	Synthetic       bool   `json:"synthetic,omitempty"`
	SyntheticReason string `json:"synthetic_reason,omitempty"`
}

type Node struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind"`
	RootID     string         `json:"root_id,omitempty"`
	Source     Source         `json:"source"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

type Edge struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind"`
	From       string         `json:"from"`
	To         string         `json:"to"`
	Source     Source         `json:"source"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

type Graph struct {
	Metadata Metadata `json:"metadata"`
	Nodes    []Node   `json:"nodes"`
	Edges    []Edge   `json:"edges"`
	Roots    []string `json:"roots"`
}

var StructuralNodeWeights = map[string]int{
	"root":            0,
	"block":           0,
	"operation":       1,
	"binding":         1,
	"assignment":      1,
	"return":          1,
	"jump":            1,
	"branch":          2,
	"multi_branch":    3,
	"case":            1,
	"loop":            3,
	"call":            2,
	"defer":           3,
	"concurrent_call": 3,
	"closure_literal": 2,
	"panic":           2,
	"recover":         2,
}

var StateNodeWeights = map[string]int{
	"parameter":      1,
	"receiver":       1,
	"result":         1,
	"local":          1,
	"temporary":      0,
	"constant":       0,
	"field":          2,
	"captured":       2,
	"global":         3,
	"external_state": 3,
}

var EdgeWeights = map[string]int{
	"containment":     0,
	"control":         1,
	"call":            2,
	"exception":       2,
	"read":            1,
	"write":           2,
	"data_dependency": 1,
	"capture":         2,
	"state_escape":    3,
	"alias":           1,
}

type Hyperparameters struct {
	StructuralDepthWeight   int `json:"structural_depth_weight"`
	StructuralBreadthWeight int `json:"structural_breadth_weight"`
	StateDepthWeight        int `json:"state_depth_weight"`
	StateBreadthWeight      int `json:"state_breadth_weight"`
	DependencyBreadthWeight int `json:"dependency_breadth_weight"`
	AliasBreadthWeight      int `json:"alias_breadth_weight"`
	ArityParameterWeight    int `json:"arity_parameter_weight"`
	ArityResultWeight       int `json:"arity_result_weight"`
	ExcessArityThreshold    int `json:"excess_arity_threshold"`
	ExcessArityWeight       int `json:"excess_arity_weight"`
	UnresolvedCallWeight    int `json:"unresolved_call_weight"`
	ExternalCallWeight      int `json:"external_call_weight"`
	StructuralCycleWeight   int `json:"structural_cycle_weight"`
	StateCycleWeight        int `json:"state_cycle_weight"`
}

func DefaultHyperparameters() Hyperparameters {
	return Hyperparameters{
		StructuralDepthWeight:   1,
		StructuralBreadthWeight: 1,
		StateDepthWeight:        1,
		StateBreadthWeight:      1,
		DependencyBreadthWeight: 1,
		AliasBreadthWeight:      1,
		ArityParameterWeight:    1,
		ArityResultWeight:       1,
		ExcessArityThreshold:    3,
		ExcessArityWeight:       2,
		UnresolvedCallWeight:    3,
		ExternalCallWeight:      1,
		StructuralCycleWeight:   4,
		StateCycleWeight:        4,
	}
}

func IsStructuralNode(kind string) bool {
	_, ok := StructuralNodeWeights[kind]
	return ok
}

func IsStateNode(kind string) bool {
	_, ok := StateNodeWeights[kind]
	return ok
}

func IsStructuralEdge(kind string) bool {
	switch kind {
	case "containment", "control", "call", "exception":
		return true
	default:
		return false
	}
}

func IsStateEdge(kind string) bool {
	switch kind {
	case "read", "write", "data_dependency", "capture", "state_escape", "alias":
		return true
	default:
		return false
	}
}
