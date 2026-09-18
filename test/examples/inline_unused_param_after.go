//go:build ignore

// An argument whose parameter the body never uses, and that has no effects,
// is dropped.
package main

import "fmt"

func main() {
	x, y := 1, 2
	fmt.Println(x+1, y)
}
