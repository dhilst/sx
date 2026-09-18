//go:build ignore

// A multi-statement function called in expression position would be inlined as
// a closure, so it is left alone.
package main

import "fmt"

func describe(n int) string {
	if n > 1 {
		return "many"
	}
	return "one"
}

func main() {
	fmt.Println(describe(2))
}
