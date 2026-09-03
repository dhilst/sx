package main

import (
	"fmt"

	"evalengine/engine"
)

func main() {
	for _, op := range append(engine.Ops(), "pow") {
		for _, acc := range []int{-5, 0, 5, 1001} {
			for _, arg := range []int{0, 3} {
				value, status := engine.Apply(op, acc, arg)
				fmt.Printf("apply(%s,%d,%d)=%d/%s\n", op, acc, arg, value, status)
			}
		}
	}
}
