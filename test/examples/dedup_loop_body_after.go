//go:build ignore

// A loop variable and an accumulator declared outside the run: the
// accumulator is a parameter and a result.
package main

import "fmt"

func a(xs []int) int {
	total := 0
	for _, x := range xs {
		scaled := x * 2
		adjusted := scaled + 1
		total += adjusted
		total += scaled
	}
	return total
}

func b(xs []int) int {
	total := 0
	for _, x := range xs {
		scaled := x * 2
		adjusted := scaled + 1
		total += adjusted
		total += scaled
	}
	return total * 2
}

func main() {
	fmt.Println(a([]int{1}), b([]int{2}))
	fmt.Println(a([]int{1}), b([]int{2}))
}
