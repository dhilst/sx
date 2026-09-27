package main

func f() int {
	x := 1
	g := func() int { return x }
	x = 2
	return g()
}
