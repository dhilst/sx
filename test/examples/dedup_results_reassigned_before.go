//go:build ignore

// A variable declared before the run and reassigned in it is both a parameter
// and a result.
package main

import "fmt"

func a(xs []int) {
	total := 0
	total += len(xs) * 3
	total -= xs[0] + xs[1]
	fmt.Println(total)
}

func b(xs []int) {
	total := 1
	total += len(xs) * 3
	total -= xs[0] + xs[1]
	fmt.Println(total * 2)
}

func main() {
	a([]int{1, 2})
	a([]int{1, 2})
	b([]int{3, 4})
	b([]int{3, 4})
}
