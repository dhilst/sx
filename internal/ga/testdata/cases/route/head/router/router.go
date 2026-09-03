package router

import "fmt"

// Commands lists the dispatchable command names.
func Commands() []string {
	return []string{"add", "sub", "mul", "div", "mod", "neg"}
}

// Route executes a command against an argument and renders the result.
func Route(cmd string, arg int) string {
	if cmd == "add" {
		if arg < 0 {
			return "error: negative argument"
		}
		return fmt.Sprintf("add=%d", arg+1)
	} else if cmd == "sub" {
		if arg < 0 {
			return "error: negative argument"
		}
		return fmt.Sprintf("sub=%d", arg-1)
	} else if cmd == "mul" {
		if arg < 0 {
			return "error: negative argument"
		}
		return fmt.Sprintf("mul=%d", arg*2)
	} else if cmd == "div" {
		if arg < 0 {
			return "error: negative argument"
		}
		if arg == 0 {
			return "error: division by zero"
		}
		return fmt.Sprintf("div=%d", 100/arg)
	} else if cmd == "mod" {
		if arg < 0 {
			return "error: negative argument"
		}
		if arg == 0 {
			return "error: division by zero"
		}
		return fmt.Sprintf("mod=%d", 100%arg)
	} else if cmd == "neg" {
		if arg < 0 {
			return "error: negative argument"
		}
		return fmt.Sprintf("neg=%d", -arg)
	} else {
		return "error: unknown command"
	}
}
