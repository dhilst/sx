package main

import "fmt"

func show(s string) *string {
	print2(s)
	return &s
}

func print2(s string) {
	fmt.Println("a", s, len(s))
	fmt.Println("b", s)
}
