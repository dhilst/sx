package main

import "fmt"

func report() {
	status := "starting"
	defer func() { fmt.Println("deferred sees:", status) }()
	fmt.Println("before:", status)
	status = "done"              //<
	fmt.Println("in R:", status) //>
}

func main() { report() }
