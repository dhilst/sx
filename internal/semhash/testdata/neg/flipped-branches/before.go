package main

func pick(c bool, a, b string) string {
	if c {
		return a
	}
	return b
}
