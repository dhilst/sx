//go:build ignore

// An unreachable function is removed.
package main

import "fmt"

func unused(n int) int {
	return n * 2
}

func main() {
	fmt.Println("hello")
}
