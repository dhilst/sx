package main

import (
	"fmt"

	"evalroute/router"
)

func main() {
	for _, cmd := range router.Commands() {
		fmt.Printf("command=%s\n", cmd)
	}
}
