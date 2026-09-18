//go:build ignore

// Free variables of several type shapes become parameters.
package main

import (
	"fmt"
	"strings"
)

func a(n int, s string, xs []byte, m map[string]int, b *strings.Builder) {
	b.WriteString(s)
	fmt.Println(n, len(xs), m[s])
}

func c(n int, s string, xs []byte, m map[string]int, b *strings.Builder) {
	b.WriteString(s)
	fmt.Println(n, len(xs), m[s])
	fmt.Println("done")
}

func main() {
	var b strings.Builder
	a(1, "x", nil, nil, &b)
	a(1, "x", nil, nil, &b)
	c(2, "y", nil, nil, &b)
	c(2, "y", nil, nil, &b)
}
