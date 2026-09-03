package main

import (
	"fmt"

	"evalgrade/classify"
)

func main() {
	for _, score := range []int{0, 55, 72, 91} {
		fmt.Printf("total(%d)=%d\n", score, classify.Total(score, 3))
	}
}
