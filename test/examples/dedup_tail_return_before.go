//go:build ignore

// A run that ends in a return always returns, so the caller returns the call.
package main

import "fmt"

func a(n int) string {
	fmt.Println("a")
	m := n * n
	if m > 10 {
		return "big"
	}
	return fmt.Sprint("small ", m)
}

func b(n int) string {
	fmt.Println("b")
	m := n * n
	if m > 10 {
		return "big"
	}
	return fmt.Sprint("small ", m)
}

func main() {
	fmt.Println(a(1), b(5))
	fmt.Println(a(1), b(5))
}
