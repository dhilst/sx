//go:build ignore

// A continue that leaves the run is threaded through a control value by
// gopls; that is not modelled, so it is not attempted.
package main

import "fmt"

func a(xs []int) {
	for _, x := range xs {
		if x < 0 {
			continue
		}
		fmt.Println("kept", x)
		fmt.Println("double", x*2)
	}
}

func b(xs []int) {
	for _, x := range xs {
		if x < 0 {
			continue
		}
		fmt.Println("kept", x)
		fmt.Println("double", x*2)
	}
}

func main() {
	a([]int{1})
	a([]int{1})
	b([]int{-1})
	b([]int{-1})
}
