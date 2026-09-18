//go:build ignore

// An argument whose parameter the body never uses, and that has no effects,
// is dropped.
package main

import "fmt"

func first(a, b int) int { return a + 1 }

func main() {
	x, y := 1, 2
	fmt.Println(first(x, y), y)
}
