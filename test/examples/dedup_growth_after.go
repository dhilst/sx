//go:build ignore

// A small run with several inputs and outputs: removing one copy saves less
// than the signature and the call cost.
package main

import "fmt"

func a(x, y int) {
	s := x + y
	d := x - y
	p := x * y
	fmt.Println(s, d, p)
}

func b(x, y int) {
	s := x + y
	d := x - y
	p := x * y
	fmt.Println(p, d, s)
}

func main() {
	a(1, 2)
	a(1, 2)
	b(3, 4)
	b(3, 4)
}
