package engine

// Ops lists the supported operations.
func Ops() []string {
	return []string{"inc", "dec", "dbl", "hlf", "sqr", "neg", "abs", "clr", "mod", "max"}
}

// Apply runs one operation against the accumulator and reports a status.
// Arms that return the same value do not always report the same status.
func Apply(op string, acc, arg int) (int, string) {
	if op == "inc" {
		if arg == 0 {
			return acc, "noop"
		}
		return acc + arg, "ok"
	} else if op == "dec" {
		if arg == 0 {
			return acc, "noop"
		}
		return acc - arg, "ok"
	} else if op == "dbl" {
		return acc * 2, "ok"
	} else if op == "hlf" {
		if acc%2 != 0 {
			return acc / 2, "truncated"
		}
		return acc / 2, "ok"
	} else if op == "sqr" {
		if acc > 1000 {
			return acc, "overflow"
		}
		return acc * acc, "ok"
	} else if op == "neg" {
		return -acc, "ok"
	} else if op == "abs" {
		if acc < 0 {
			return -acc, "ok"
		}
		return acc, "noop"
	} else if op == "clr" {
		if acc == 0 {
			return 0, "noop"
		}
		return 0, "ok"
	} else if op == "mod" {
		if arg == 0 {
			return acc, "divzero"
		}
		return acc % arg, "ok"
	} else if op == "max" {
		if arg > acc {
			return arg, "ok"
		}
		return acc, "noop"
	} else {
		return acc, "unknown"
	}
}
