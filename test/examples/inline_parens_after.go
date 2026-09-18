//go:build ignore

// An argument that is an operation needs parentheses where the body uses the
// parameter as an operand of a tighter operator.
package main

import "fmt"

func main() {
	a, b := 1, 2
	fmt.Println((a + b) * 2)
}
