//go:build ignore

// Inside a generic function the parameter and result types are type
// parameters.
package main

import "fmt"

func a[T any](xs []T) {
	first := xs[0]
	fmt.Println("first", first)
	fmt.Println("count", len(xs))
	fmt.Println(first)
}

func b[T any](xs []T) {
	first := xs[0]
	fmt.Println("first", first)
	fmt.Println("count", len(xs))
	fmt.Println(first, first)
}

func main() {
	a([]int{1})
	a([]int{1})
	b([]string{"x"})
	b([]string{"x"})
}
