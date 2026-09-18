//go:build ignore

// An unexported function called once, in statement position, is inlined.
package main

import "fmt"

func main() {
	var name string = "world"
	fmt.Println("hello", name)
	fmt.Println("bye", name)
}
