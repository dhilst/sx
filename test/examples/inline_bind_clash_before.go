//go:build ignore

// The body declares a name the caller already has, so the inlined statements
// keep their braces.
package main

import "fmt"

func report(n int) {
	m := n + 1
	fmt.Println("next", m)
}

func main() {
	m := 2
	report(m)
	fmt.Println(m)
}
