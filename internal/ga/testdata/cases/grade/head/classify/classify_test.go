package classify

import "testing"

func TestTotal(t *testing.T) {
	if got := Total(70, 5); got != 75 {
		t.Fatalf("Total(70,5) = %d, want 75", got)
	}
}

func TestGrade(t *testing.T) {
	cases := []struct {
		score    int
		attended bool
		extra    int
		want     string
	}{
		{-1, true, 0, "invalid"},
		{101, true, 0, "invalid"},
		{95, true, 0, "A"},
		{85, true, 0, "B"},
		{75, true, 0, "C"},
		{65, true, 0, "D"},
		{10, true, 0, "F"},
		{85, true, 5, "A"},
		{95, false, 0, "A-"},
		{85, false, 0, "B-"},
		{75, false, 0, "C-"},
		{65, false, 0, "D-"},
		{10, false, 0, "F"},
		{85, false, 20, "B-"},
	}
	for _, c := range cases {
		if got := Grade(c.score, c.attended, c.extra); got != c.want {
			t.Errorf("Grade(%d,%v,%d) = %q, want %q", c.score, c.attended, c.extra, got, c.want)
		}
	}
}
