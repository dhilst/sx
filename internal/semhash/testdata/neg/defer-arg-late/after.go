package main

import "fmt"

func work() {
	i := 1
	defer func() { fmt.Println(i) }()
	i = 2
	fmt.Println("now", i)
}
