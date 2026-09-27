//go:build ignore

// A function well under √2·B. It stays as it is: splitting it would cost a
// declaration and a call and save less reading than that.
package main

import "fmt"

func classify(xs []int) (neg, zero, pos int) {
	for _, x := range xs {
		switch {
		case x < 0:
			neg++
		case x == 0:
			zero++
		default:
			pos++
		}
	}
	return neg, zero, pos
}

func main() {
	fmt.Println(classify([]int{-1, 0, 2, 3}))
}
