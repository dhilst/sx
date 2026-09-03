package main

import (
	"fmt"

	"evalsummary/render"
)

func main() {
	fmt.Printf("label=%s\n", render.Label(""))
	fmt.Printf("label=%s\n", render.Label("build"))
	for _, name := range []string{"build", ""} {
		for _, items := range [][]string{nil, {"a"}, {"a", "b", "c"}} {
			for _, failures := range []int{0, 1, 4} {
				fmt.Printf("summary=%s\n", render.Summary(name, items, failures))
			}
		}
	}
}
