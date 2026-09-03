package classify

import "testing"

func TestTotal(t *testing.T) {
	if got := Total(70, 5); got != 75 {
		t.Fatalf("Total(70,5) = %d, want 75", got)
	}
}
