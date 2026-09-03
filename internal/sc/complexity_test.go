package sc

import (
	"testing"

	"purgatrix/internal/sx"
)

func TestNormativeExamples(t *testing.T) {
	tests := []struct {
		name string
		g    *sx.Graph
		want int
	}{
		{"one operation", exampleOneOperation(), 1},
		{"four siblings", exampleFourSiblings(), 7},
		{"binary branch", exampleBinaryBranch(), 14},
		{"arity", exampleArity(), 18},
		{"state chain", exampleStateChain(), 8},
		{"state breadth", exampleStateBreadth(), 19},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report, err := Complexity(tt.g)
			if err != nil {
				t.Fatal(err)
			}
			if report.Total != tt.want {
				t.Fatalf("Total = %d, want %d\n%+v", report.Total, tt.want, report)
			}
		})
	}
}

func baseGraph(nodes []sx.Node, edges []sx.Edge) *sx.Graph {
	return &sx.Graph{
		Metadata: sx.Metadata{
			SchemaVersion:          sx.SchemaVersion,
			ModelVersion:           sx.ModelVersion,
			FrontendID:             "test",
			FrontendVersion:        "test",
			TranslationSpecVersion: "test",
			SourceUnitID:           "test",
		},
		Nodes: nodes,
		Edges: edges,
		Roots: []string{"r"},
	}
}

func root(attrs map[string]any) sx.Node {
	if attrs == nil {
		attrs = map[string]any{}
	}
	attrs["root_kind"] = "function"
	attrs["identity"] = "test.f"
	if _, ok := attrs["parameter_count"]; !ok {
		attrs["parameter_count"] = 0
	}
	if _, ok := attrs["receiver_count"]; !ok {
		attrs["receiver_count"] = 0
	}
	if _, ok := attrs["result_count"]; !ok {
		attrs["result_count"] = 0
	}
	return sx.Node{ID: "r", Kind: "root", Source: testSource(), Attributes: attrs}
}

func sn(id, kind string) sx.Node {
	return sx.Node{ID: id, Kind: kind, RootID: "r", Source: testSource()}
}

func st(id, kind string) sx.Node {
	return sx.Node{ID: id, Kind: kind, RootID: "r", Source: testSource(), Attributes: map[string]any{"owner_root_id": "r"}}
}

func edge(id, kind, from, to string) sx.Edge {
	return sx.Edge{ID: id, Kind: kind, From: from, To: to, Source: testSource()}
}

func testSource() sx.Source {
	return sx.Source{Path: "test.go", StartLine: 1, StartColumn: 1, EndLine: 1, EndColumn: 2}
}

func exampleOneOperation() *sx.Graph {
	return baseGraph([]sx.Node{root(nil), sn("n", "operation")}, []sx.Edge{edge("e1", "containment", "r", "n")})
}

func exampleFourSiblings() *sx.Graph {
	return baseGraph(
		[]sx.Node{root(nil), sn("a", "operation"), sn("b", "operation"), sn("c", "operation"), sn("d", "operation")},
		[]sx.Edge{
			edge("e1", "containment", "r", "a"),
			edge("e2", "containment", "r", "b"),
			edge("e3", "containment", "r", "c"),
			edge("e4", "containment", "r", "d"),
		},
	)
}

func exampleBinaryBranch() *sx.Graph {
	return baseGraph(
		[]sx.Node{root(nil), sn("br", "branch"), sn("yes", "case"), sn("no", "case"), sn("a", "operation"), sn("b", "operation")},
		[]sx.Edge{
			edge("e1", "containment", "r", "br"),
			edge("e2", "containment", "br", "yes"),
			edge("e3", "containment", "br", "no"),
			edge("e4", "containment", "yes", "a"),
			edge("e5", "containment", "no", "b"),
		},
	)
}

func exampleArity() *sx.Graph {
	return baseGraph(
		[]sx.Node{
			root(map[string]any{"parameter_count": 5, "result_count": 1}),
			st("p1", "parameter"), st("p2", "parameter"), st("p3", "parameter"), st("p4", "parameter"), st("p5", "parameter"), st("out", "result"),
		},
		nil,
	)
}

func exampleStateChain() *sx.Graph {
	return baseGraph(
		[]sx.Node{root(nil), st("p", "parameter"), st("x", "local"), st("y", "local")},
		[]sx.Edge{edge("e1", "data_dependency", "p", "x"), edge("e2", "data_dependency", "x", "y")},
	)
}

func exampleStateBreadth() *sx.Graph {
	return baseGraph(
		[]sx.Node{root(nil), sn("op", "operation"), st("a", "parameter"), st("b", "parameter"), st("c", "parameter"), st("out", "local")},
		[]sx.Edge{
			edge("e1", "containment", "r", "op"),
			edge("e2", "read", "op", "a"),
			edge("e3", "read", "op", "b"),
			edge("e4", "read", "op", "c"),
			edge("e5", "write", "op", "out"),
			edge("e6", "data_dependency", "a", "out"),
			edge("e7", "data_dependency", "b", "out"),
			edge("e8", "data_dependency", "c", "out"),
		},
	)
}
