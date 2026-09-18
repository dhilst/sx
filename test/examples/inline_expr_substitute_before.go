//go:build ignore

// A single returned expression in expression position: each parameter is
// replaced by its argument.
package main

import "fmt"

func twice(n int) int { return n * 2 }

func main() {
	x := 4
	fmt.Println(twice(x))
}
