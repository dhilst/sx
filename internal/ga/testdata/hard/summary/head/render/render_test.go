package render

import "testing"

func TestLabel(t *testing.T) {
	if Label("") != "(unnamed)" || Label("x") != "x" {
		t.Fatal("Label misrendered")
	}
}

func TestSummary(t *testing.T) {
	one := []string{"a"}
	two := []string{"a", "b"}
	cases := []struct {
		name     string
		items    []string
		failures int
		want     string
	}{
		{"build", nil, 0, "build: nothing to do"},
		{"build", nil, 1, "build: nothing to do, 1 failure"},
		{"build", nil, 3, "build: nothing to do, 3 failures"},
		{"build", one, 0, "build: 1 item"},
		{"build", one, 1, "build: 1 item, 1 failure"},
		{"build", one, 2, "build: 1 item, 2 failures"},
		{"build", two, 0, "build: 2 items"},
		{"build", two, 1, "build: 2 items, 1 failure"},
		{"build", two, 5, "build: 2 items, 5 failures"},
		{"", two, 1, "(unnamed): 2 items, 1 failure"},
	}
	for _, c := range cases {
		if got := Summary(c.name, c.items, c.failures); got != c.want {
			t.Errorf("Summary(%q,%v,%d) = %q, want %q", c.name, c.items, c.failures, got, c.want)
		}
	}
}
