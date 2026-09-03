package router

import "fmt"

// Commands lists the dispatchable command names.
func Commands() []string {
	return []string{"add", "sub", "mul", "div", "mod", "neg"}
}

// Route executes a command against an argument and renders the result.
func Route(cmd string, arg int) string {
	switch cmd {
	case "add", "sub", "mul", "div", "mod", "neg":
	default:
		return "error: unknown command"
	}
	if arg < 0 {
		return "error: negative argument"
	}
	switch cmd {
	case "add":
		return fmt.Sprintf("add=%d", arg+1)
	case "sub":
		return fmt.Sprintf("sub=%d", arg-1)
	case "mul":
		return fmt.Sprintf("mul=%d", arg*2)
	case "neg":
		return fmt.Sprintf("neg=%d", -arg)
	}
	if arg == 0 {
		return "error: division by zero"
	}
	if cmd == "div" {
		return fmt.Sprintf("div=%d", 100/arg)
	}
	return fmt.Sprintf("mod=%d", 100%arg)
}
