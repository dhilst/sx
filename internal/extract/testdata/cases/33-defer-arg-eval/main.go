package main

import "fmt"

func main() {
	i := 1
	defer fmt.Println("deferred sees", i) //<
	i = 2                                 //>
	i = 3
	fmt.Println("now", i)
}
