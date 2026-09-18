//go:build ignore

// Results pay for themselves when the run is big enough and repeated often
// enough.
package main

import (
	"fmt"
	"strings"
)

func a(s string) {
	fields := strings.Fields(strings.TrimSpace(s))
	joined := strings.Join(fields, "-")
	fmt.Println("fields", len(fields), fields)
	fmt.Println("joined", joined, strings.ToUpper(joined))
	fmt.Println(joined)
}

func b(s string) {
	fields := strings.Fields(strings.TrimSpace(s))
	joined := strings.Join(fields, "-")
	fmt.Println("fields", len(fields), fields)
	fmt.Println("joined", joined, strings.ToUpper(joined))
	fmt.Println(joined, joined)
}

func c(s string) {
	fields := strings.Fields(strings.TrimSpace(s))
	joined := strings.Join(fields, "-")
	fmt.Println("fields", len(fields), fields)
	fmt.Println("joined", joined, strings.ToUpper(joined))
	fmt.Println(len(joined))
}

func main() {
	a("x y")
	a("x y")
	b("y z")
	b("y z")
	c("z w")
	c("z w")
}
