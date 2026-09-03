package config

import "testing"

func TestNormalize(t *testing.T) {
	if got := Normalize("  a  "); got != "a" {
		t.Fatalf("Normalize = %q, want \"a\"", got)
	}
}
