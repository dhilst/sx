//go:build ignore

// The run is the body of a case clause rather than a block.
package main

import "fmt"

func a(k string, n int) {
	switch k {
	case "x":
		fmt.Println("x chosen")
		fmt.Println(n + 1)
	case "y":
		fmt.Println("x chosen")
		fmt.Println(n + 1)
	}
}

func main() {
	a("x", 1)
	a("x", 1)
}
