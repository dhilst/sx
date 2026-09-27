package main

import "fmt"

func work() {
	newFunction()
	fmt.Println("more")
}

func newFunction() {
	defer fmt.Println("cleanup")
	fmt.Println("work")
}
