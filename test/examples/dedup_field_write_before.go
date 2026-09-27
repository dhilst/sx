//go:build ignore

// The copies write fields of a struct declared before them. gopls would pass
// the struct to the new function by value and hand nothing back, so the
// writes would land on a copy; the model refuses the run.
package main

import "fmt"

type stats struct {
	count, total int
	label        string
}

func a(xs []int) stats {
	s := stats{label: "a"}
	s.count = len(xs)
	s.total = s.count * 2
	s.label += fmt.Sprint(s.total)
	return s
}

func b(xs []int) stats {
	s := stats{label: "b"}
	s.count = len(xs)
	s.total = s.count * 2
	s.label += fmt.Sprint(s.total)
	s.count++
	return s
}

func main() {
	fmt.Println(a([]int{1, 2}), b([]int{3}))
}
