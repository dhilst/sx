package engine

import "testing"

func TestOps(t *testing.T) {
	if len(Ops()) != 10 {
		t.Fatalf("len(Ops()) = %d, want 10", len(Ops()))
	}
}

func TestApply(t *testing.T) {
	cases := []struct {
		op         string
		acc, arg   int
		wantValue  int
		wantStatus string
	}{
		{"inc", 5, 2, 7, "ok"},
		{"inc", 5, 0, 5, "noop"},
		{"dec", 5, 2, 3, "ok"},
		{"dec", 5, 0, 5, "noop"},
		{"dbl", 5, 0, 10, "ok"},
		{"hlf", 4, 0, 2, "ok"},
		{"hlf", 5, 0, 2, "truncated"},
		{"hlf", -5, 0, -2, "truncated"},
		{"sqr", 5, 0, 25, "ok"},
		{"sqr", 1001, 0, 1001, "overflow"},
		{"neg", 5, 0, -5, "ok"},
		{"abs", -5, 0, 5, "ok"},
		{"abs", 5, 0, 5, "noop"},
		{"clr", 5, 0, 0, "ok"},
		{"clr", 0, 0, 0, "noop"},
		{"mod", 5, 3, 2, "ok"},
		{"mod", 5, 0, 5, "divzero"},
		{"max", 5, 9, 9, "ok"},
		{"max", 5, 1, 5, "noop"},
		{"pow", 5, 2, 5, "unknown"},
	}
	for _, c := range cases {
		value, status := Apply(c.op, c.acc, c.arg)
		if value != c.wantValue || status != c.wantStatus {
			t.Errorf("Apply(%q,%d,%d) = (%d,%q), want (%d,%q)", c.op, c.acc, c.arg, value, status, c.wantValue, c.wantStatus)
		}
	}
}
