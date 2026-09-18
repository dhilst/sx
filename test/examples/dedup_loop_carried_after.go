//go:build ignore

// A write the run makes to a variable a loop reads on its next iteration
// must not be dropped, even though nothing after the run in the source reads
// it.
package main

import "fmt"

func a(xs []int) {
	newFunction(xs)
}

func newFunction(xs []int) {
	prev := 0
	for _, x := range xs {
		fmt.Println("delta", x-prev)
		prev = x
		fmt.Println("seen", x, x*x)
	}
}

func b(xs []int) {
	newFunction(xs)
}

func main() {
	a([]int{1, 2})
	a([]int{1, 2})
	b([]int{3, 4})
	b([]int{3, 4})
}
