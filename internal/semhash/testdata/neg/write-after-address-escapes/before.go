package main

var g *int

func f() int {
	x := 1
	g = &x
	x = 2
	return *g
}
