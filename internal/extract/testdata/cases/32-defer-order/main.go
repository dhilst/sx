package main

import "fmt"

func work() {
	defer fmt.Println("registered before R")
	fmt.Println("start")
	defer fmt.Println("in R: first") //<
	defer fmt.Println("in R: second")
	fmt.Println("R body") //>
	defer fmt.Println("registered after R")
	fmt.Println("end")
}

func main() { work() }
