//go:build ignore

// Three copies: every extra copy saves B and costs C.
package main

import "fmt"

func a(n int) {
	newFunction(n)
}

func newFunction(n int) {
	fmt.Println("value", n)
	fmt.Println("double", n*2)
}

func b(n int) {
	newFunction(n)
}

func c(n int) {
	newFunction(n)
}

func main() {
	a(1)
	a(1)
	b(2)
	b(2)
	c(3)
	c(3)
}
