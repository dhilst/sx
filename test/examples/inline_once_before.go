//go:build ignore

// An unexported function called once, in statement position, is inlined.
package main

import "fmt"

func greet(name string) {
	fmt.Println("hello", name)
	fmt.Println("bye", name)
}

func main() {
	greet("world")
}
