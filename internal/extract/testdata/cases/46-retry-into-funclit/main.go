package main

import "fmt"

func outer(xs []int) (int, error) {
	sum := func(ys []int) int {
		t := 0 //<
		for _, y := range ys {
			t += y
		}
		return t
	}
	return sum(xs), nil //>
}

func main() { fmt.Println(outer([]int{1, 2, 3})) }
