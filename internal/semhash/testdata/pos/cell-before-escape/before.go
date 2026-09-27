package main

import "fmt"

func show(s string) *string {
	fmt.Println("a", s, len(s))
	fmt.Println("b", s)
	return &s
}
