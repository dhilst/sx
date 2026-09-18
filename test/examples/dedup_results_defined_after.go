//go:build ignore

// Variables the run declares and the caller uses afterwards come back as
// results, and the call declares them with :=.
package main

import (
	"fmt"
	"strings"
)

func a(s string) {
	upper := strings.ToUpper(s)
	lower := strings.ToLower(s)
	fmt.Println(upper, lower)
}

func b(s string) {
	upper := strings.ToUpper(s)
	lower := strings.ToLower(s)
	fmt.Println(lower, upper)
}

func main() {
	a("x")
	a("x")
	b("y")
	b("y")
}
