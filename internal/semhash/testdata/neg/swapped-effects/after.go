package main

import "fmt"

func greet() {
	newFunction()
	fmt.Println("hello")
}

func newFunction() { fmt.Println("world") }
