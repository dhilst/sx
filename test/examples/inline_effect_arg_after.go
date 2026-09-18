//go:build ignore

// An argument with effects, referenced once, is still substituted.
package main

import "fmt"

func main() {
	fmt.Println("value", 3)
	fmt.Println("done")
}
