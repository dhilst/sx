package main

import (
	"fmt"

	"evalroute/router"
)

func main() {
	for _, cmd := range append(router.Commands(), "pow", "") {
		for _, arg := range []int{-2, 0, 3, 7} {
			fmt.Printf("route(%s,%d)=%s\n", cmd, arg, router.Route(cmd, arg))
		}
	}
}
