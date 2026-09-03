package main

import (
	"fmt"

	"evalengine/engine"
)

func main() {
	for _, op := range engine.Ops() {
		fmt.Printf("op=%s\n", op)
	}
}
