package main

var g *int

func f() int {
	x := 1
	g = &x
	return *g
}
