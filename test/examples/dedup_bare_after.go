//go:build ignore

// A run with no inputs and no outputs becomes a bare call.
package main

import "fmt"

func a() {
	newFunction()
}

func newFunction() {
	fmt.Println("starting")
	fmt.Println("loading config")
	fmt.Println("ready")
}

func b() {
	newFunction()
}

func main() {
	a()
	a()
	b()
	b()
}
