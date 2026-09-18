//go:build ignore

// strings.Index(s, sub) >= 0 is strings.Contains(s, sub).
package main

import (
	"fmt"
	"strings"
)

func main() {
	s := "hello"
	fmt.Println(strings.Index(s, "ll") >= 0)
}
