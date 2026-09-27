package main

import "fmt"

func retry() int {
	n := 0
again:
	n = step(n)
	if n < 3 {
		goto again
	}
	return n
}

func step(n int) int {
	n++
	fmt.Println("try", n)
	return n
}
