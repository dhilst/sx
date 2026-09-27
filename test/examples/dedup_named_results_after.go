//go:build ignore

// The enclosing function has named results; the flag case returns them.
package main

import "fmt"

func a(n int) (label string, ok bool) {
	if n == 0 {
		return "zero", false
	}
	newFunction(n)
	return "n", true
}

func newFunction(n int) {
	fmt.Println("nonzero", n)
	fmt.Println("half", n/2)
}

func b(n int) (label string, ok bool) {
	if n == 0 {
		return "zero", false
	}
	newFunction(n)
	return "m", true
}

func main() {
	fmt.Println(a(1))
	fmt.Println(a(1))
	fmt.Println(b(0))
	fmt.Println(b(0))
}
