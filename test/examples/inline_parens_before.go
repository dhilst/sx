//go:build ignore

// An argument that is an operation needs parentheses where the body uses the
// parameter as an operand of a tighter operator.
package main

import "fmt"

func double(n int) int { return n * 2 }

func main() {
	a, b := 1, 2
	fmt.Println(double(a + b))
}
