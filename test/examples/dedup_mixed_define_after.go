//go:build ignore

// The run declares one result and reassigns another declared in an outer
// scope, so the call cannot use := and the new one is declared with var.
package main

import "fmt"

func a(xs []int) {
	count := 0
	if len(xs) > 0 {
		first := xs[0] * 7
		count = first + len(xs)
		fmt.Println(first, count)
	}
}

func b(xs []int) {
	count := 0
	if len(xs) > 1 {
		first := xs[0] * 7
		count = first + len(xs)
		fmt.Println(count, first)
	}
}

func main() {
	a([]int{1})
	a([]int{1})
	b([]int{1, 2})
	b([]int{1, 2})
}
