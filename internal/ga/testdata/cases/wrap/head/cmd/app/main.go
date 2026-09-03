package main

import (
	"fmt"

	"evalwrap/pipeline"
)

func main() {
	fmt.Printf("sum=%d\n", pipeline.Sum([]int{1, 2, 3, 4}))
	for _, factor := range []int{0, 1, 3, -2} {
		fmt.Printf("run(%d)=%d\n", factor, pipeline.Run([]int{1, 2, 3, 4}, factor))
	}
}
