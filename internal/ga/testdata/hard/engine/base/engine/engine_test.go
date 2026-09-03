package engine

import "testing"

func TestOps(t *testing.T) {
	if len(Ops()) != 10 {
		t.Fatalf("len(Ops()) = %d, want 10", len(Ops()))
	}
}
