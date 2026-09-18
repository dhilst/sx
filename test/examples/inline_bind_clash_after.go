//go:build ignore

// The body declares a name the caller already has, so the inlined statements
// keep their braces.
package main

import "fmt"

func main() {
	m := 2
	{
		var n int = m
		m := n + 1
		fmt.Println("next", m)
	}
	fmt.Println(m)
}
