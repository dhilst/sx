//go:build ignore

// The code after one copy reads body; the code after the other does not. The
// call gopls writes for the first copy declares body at the second, where
// nothing uses it.
package main

import (
	"fmt"
	"strings"
)

func a(s string) {
	body := strings.TrimSpace(s)
	fmt.Println("trimmed", len(body), strings.ToUpper(body))
	fmt.Println("original", len(s))
	fmt.Println(body)
}

func b(s string) {
	body := strings.TrimSpace(s)
	fmt.Println("trimmed", len(body), strings.ToUpper(body))
	fmt.Println("original", len(s))
}

func main() {
	a(" x ")
	a(" y ")
	b(" x ")
	b(" y ")
}
