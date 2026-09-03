package main

import (
	"fmt"

	"evalparse/config"
)

func main() {
	fmt.Printf("normalize=%q\n", config.Normalize("  spaced  "))
}
