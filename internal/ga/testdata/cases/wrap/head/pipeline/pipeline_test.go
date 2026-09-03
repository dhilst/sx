package pipeline

import "testing"

func TestSum(t *testing.T) {
	if got := Sum([]int{1, 2, 3}); got != 6 {
		t.Fatalf("Sum = %d, want 6", got)
	}
}

func TestRun(t *testing.T) {
	cases := []struct {
		xs     []int
		factor int
		want   int
	}{
		{nil, 3, 0},
		{[]int{}, 3, 0},
		{[]int{1, 2, 3}, 1, 6},
		{[]int{1, 2, 3}, 2, 12},
		{[]int{1, 2, 3}, 0, 0},
		{[]int{-1, 2, -3}, 3, -6},
	}
	for _, c := range cases {
		if got := Run(c.xs, c.factor); got != c.want {
			t.Errorf("Run(%v,%d) = %d, want %d", c.xs, c.factor, got, c.want)
		}
	}
}
