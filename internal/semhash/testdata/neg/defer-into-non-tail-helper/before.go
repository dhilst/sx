package main

import "fmt"

func work() {
	defer fmt.Println("cleanup")
	fmt.Println("work")
	fmt.Println("more")
}
