package main

import "fmt"

func work() {
	i := 1
	defer fmt.Println(i)
	i = 2
	fmt.Println("now", i)
}
