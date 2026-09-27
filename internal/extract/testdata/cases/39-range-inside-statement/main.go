package main

import "fmt"

func main() {
	total := 0
	for i := 0; i < 3; i++ {
		total += compute(
			i,   //<
			i*2, //>
		)
	}
	fmt.Println(total)
}

func compute(a, b int) int { return a + b }
