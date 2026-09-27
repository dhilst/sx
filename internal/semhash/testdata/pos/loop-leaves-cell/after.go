package main

import (
	"fmt"
	"io"
)

func f(xs []int, w io.Writer) int {
	t := 1
	t = newFunction(xs, w)
	fmt.Fprintln(w, "done")
	return *(&t)
}

func newFunction(xs []int, w io.Writer) int {
	for _, x := range xs {
		fmt.Fprintln(w, x)
	}
	return 2
}
