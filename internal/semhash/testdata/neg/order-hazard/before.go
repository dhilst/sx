package main

var g = 1

func bump() int { g++; return g }

func pair() []int { return []int{g, bump()} }
