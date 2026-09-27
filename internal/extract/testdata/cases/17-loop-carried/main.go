package main

import "fmt"

func main() {
	prev, fib := 0, 1
	for i := 0; i < 10; i++ {
		next := prev + fib //<
		prev = fib
		fib = next //>
	}
	fmt.Println(fib)
}
