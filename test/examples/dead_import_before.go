//go:build ignore

// The unreachable function is the only user of an import, which goes with it.
package main

import (
	"fmt"
	"strings"
)

func shout(s string) string {
	return strings.ToUpper(s)
}

func main() {
	fmt.Println("hello")
}
