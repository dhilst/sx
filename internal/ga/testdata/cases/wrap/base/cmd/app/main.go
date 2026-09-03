package main

import (
	"fmt"

	"evalwrap/pipeline"
)

func main() {
	fmt.Printf("sum=%d\n", pipeline.Sum([]int{1, 2, 3, 4}))
}
