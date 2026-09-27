package main

var g = 1

func bump() int { g++; return g }

func pair() []int {
	a := g
	return []int{a, bump()}
}
