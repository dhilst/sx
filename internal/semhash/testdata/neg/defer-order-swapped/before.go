package main

import "fmt"

func work() {
	defer fmt.Println("first registered")
	defer fmt.Println("second registered")
	fmt.Println("work")
}
