package main

import (
	"fmt"

	"evalgrade/classify"
)

func main() {
	for _, score := range []int{-5, 0, 55, 65, 72, 84, 91, 130} {
		for _, attended := range []bool{true, false} {
			for _, extra := range []int{0, 7} {
				fmt.Printf("grade(%d,%v,%d)=%s\n", score, attended, extra, classify.Grade(score, attended, extra))
			}
		}
	}
	for _, score := range []int{0, 55, 72, 91} {
		fmt.Printf("total(%d)=%d\n", score, classify.Total(score, 3))
	}
}
