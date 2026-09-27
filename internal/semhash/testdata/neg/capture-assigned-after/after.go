package main

func f() int {
	x := 1
	y := x
	g := func() int { return y }
	x = 2
	return g()
}
