package router

import "testing"

func TestCommands(t *testing.T) {
	if got := len(Commands()); got != 6 {
		t.Fatalf("len(Commands()) = %d, want 6", got)
	}
}

func TestRoute(t *testing.T) {
	cases := []struct {
		cmd  string
		arg  int
		want string
	}{
		{"add", 4, "add=5"},
		{"sub", 4, "sub=3"},
		{"mul", 4, "mul=8"},
		{"div", 4, "div=25"},
		{"mod", 3, "mod=1"},
		{"neg", 4, "neg=-4"},
		{"div", 0, "error: division by zero"},
		{"mod", 0, "error: division by zero"},
		{"add", -1, "error: negative argument"},
		{"neg", -1, "error: negative argument"},
		{"pow", 4, "error: unknown command"},
		{"", 0, "error: unknown command"},
	}
	for _, c := range cases {
		if got := Route(c.cmd, c.arg); got != c.want {
			t.Errorf("Route(%q,%d) = %q, want %q", c.cmd, c.arg, got, c.want)
		}
	}
}
