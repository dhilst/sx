package main

import "fmt"

func retry() int {
	n := 0
again:
	n++
	fmt.Println("try", n)
	if n < 4 {
		goto again
	}
	return n
}
