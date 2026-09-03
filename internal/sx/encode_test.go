package sx

import (
	"bytes"
	"strings"
	"testing"
)

func TestRoundTripDeterministic(t *testing.T) {
	g := minimalGraph()
	var first bytes.Buffer
	if err := Encode(&first, g); err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(bytes.NewReader(first.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	var second bytes.Buffer
	if err := Encode(&second, decoded); err != nil {
		t.Fatal(err)
	}
	if first.String() != second.String() {
		t.Fatalf("round-trip changed canonical encoding\nfirst:\n%s\nsecond:\n%s", first.String(), second.String())
	}
}

func TestValidateRejectsUnknownKind(t *testing.T) {
	g := minimalGraph()
	g.Nodes = append(g.Nodes, Node{ID: "bad", Kind: "mystery", Source: src()})
	err := Validate(g)
	if err == nil || !strings.Contains(err.Error(), "SC-VAL-3") {
		t.Fatalf("expected SC-VAL-3, got %v", err)
	}
}

func minimalGraph() *Graph {
	return &Graph{
		Metadata: Metadata{
			SchemaVersion:          SchemaVersion,
			ModelVersion:           ModelVersion,
			FrontendID:             "test",
			FrontendVersion:        "test",
			TranslationSpecVersion: "test",
			SourceUnitID:           "test.sx",
		},
		Nodes: []Node{{
			ID:     "root:test",
			Kind:   "root",
			Source: src(),
			Attributes: map[string]any{
				"root_kind":       "function",
				"identity":        "test.f",
				"parameter_count": 0,
				"receiver_count":  0,
				"result_count":    0,
			},
		}},
		Roots: []string{"root:test"},
	}
}

func src() Source {
	return Source{Path: "test.go", StartLine: 1, StartColumn: 1, EndLine: 1, EndColumn: 2}
}
