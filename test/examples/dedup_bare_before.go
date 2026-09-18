//go:build ignore

// A run with no inputs and no outputs becomes a bare call.
package main

import "fmt"

func a() {
	fmt.Println("starting")
	fmt.Println("loading config")
	fmt.Println("ready")
}

func b() {
	fmt.Println("starting")
	fmt.Println("loading config")
	fmt.Println("ready")
}

func main() {
	a()
	a()
	b()
	b()
}
