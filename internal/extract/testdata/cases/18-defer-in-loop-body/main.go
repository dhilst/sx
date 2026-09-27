package main

import "fmt"

func main() {
	for i := 0; i < 3; i++ {
		defer fmt.Println("deferred", i) //<
		fmt.Println("body", i)           //>
	}
	fmt.Println("after loop")
}
