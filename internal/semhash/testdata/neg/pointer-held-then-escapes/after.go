package main

var g *int

func f() int {
	x := 1
	p := &x
	x = 2
	g = p
	return *g
}
