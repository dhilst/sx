package render

import "testing"

func TestLabel(t *testing.T) {
	if Label("") != "(unnamed)" || Label("x") != "x" {
		t.Fatal("Label misrendered")
	}
}
