package main

import "fmt"

func work() {
	defer fmt.Println("second registered")
	defer fmt.Println("first registered")
	fmt.Println("work")
}
