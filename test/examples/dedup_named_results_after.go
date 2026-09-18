//go:build ignore

// The enclosing function has named results; the flag case returns them.
package main

import "fmt"

func a(n int) (label string, ok bool) {
	if n == 0 {
		return "zero", false
	}
	fmt.Println("nonzero", n)
	fmt.Println("half", n/2)
	return "n", true
}

func b(n int) (label string, ok bool) {
	if n == 0 {
		return "zero", false
	}
	fmt.Println("nonzero", n)
	fmt.Println("half", n/2)
	return "m", true
}

func main() {
	fmt.Println(a(1))
	fmt.Println(a(1))
	fmt.Println(b(0))
	fmt.Println(b(0))
}
