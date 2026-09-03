package engine

// Ops lists the supported operations.
func Ops() []string {
	return []string{"inc", "dec", "dbl", "hlf", "sqr", "neg", "abs", "clr", "mod", "max"}
}

// Apply runs one operation against the accumulator and reports a status.
// Arms that return the same value do not always report the same status.
func Apply(op string, acc, arg int) (int, string) {
	switch op {
	case "inc":
		if arg == 0 {
			return acc, "noop"
		}
		return acc + arg, "ok"
	case "dec":
		if arg == 0 {
			return acc, "noop"
		}
		return acc - arg, "ok"
	case "dbl":
		return acc * 2, "ok"
	case "hlf":
		if acc%2 != 0 {
			return acc / 2, "truncated"
		}
		return acc / 2, "ok"
	case "sqr":
		if acc > 1000 {
			return acc, "overflow"
		}
		return acc * acc, "ok"
	case "neg":
		return -acc, "ok"
	case "abs":
		if acc < 0 {
			return -acc, "ok"
		}
		return acc, "noop"
	case "clr":
		if acc == 0 {
			return 0, "noop"
		}
		return 0, "ok"
	case "mod":
		if arg == 0 {
			return acc, "divzero"
		}
		return acc % arg, "ok"
	case "max":
		if arg > acc {
			return arg, "ok"
		}
		return acc, "noop"
	default:
		return acc, "unknown"
	}
}
