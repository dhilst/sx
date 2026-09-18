//go:build ignore

// In a the run declares err; in b the same text reuses an err declared
// before it. The call gopls writes for a, err := newFunction(...), declares
// nothing new in b.
package main

import (
	"fmt"
	"strconv"
)

func a(s string) error {
	n, err := strconv.Atoi(s)
	fmt.Println("parsed", n, len(s))
	fmt.Println("twice", n*2, s)
	return err
}

func b(s string) error {
	var err error
	n, err := strconv.Atoi(s)
	fmt.Println("parsed", n, len(s))
	fmt.Println("twice", n*2, s)
	return err
}

func main() {
	fmt.Println(a("1"), a("2"), b("3"), b("4"))
}
