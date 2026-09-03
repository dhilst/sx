package router

import "testing"

func TestCommands(t *testing.T) {
	if got := len(Commands()); got != 6 {
		t.Fatalf("len(Commands()) = %d, want 6", got)
	}
}
