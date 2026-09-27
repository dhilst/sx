package main

import (
	"fmt"
	"io"
)

func f(xs []int, w io.Writer) int {
	t := 1
	for _, x := range xs {
		fmt.Fprintln(w, x)
	}
	t = 2
	fmt.Fprintln(w, "done")
	return *(&t)
}
