package main

import (
	"fmt"

	"evalsummary/render"
)

func main() {
	fmt.Printf("label=%s\n", render.Label(""))
	fmt.Printf("label=%s\n", render.Label("build"))
}
