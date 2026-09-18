//go:build ignore

// The unreachable function is the only user of an import, which goes with it.
package main

import (
	"fmt"
)

func main() {
	fmt.Println("hello")
}
