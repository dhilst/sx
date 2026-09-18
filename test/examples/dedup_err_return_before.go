//go:build ignore

// Every return in the run is "if err != nil { return ..., err }", so the caller
// checks the error instead of a flag.
package main

import (
	"fmt"
	"strconv"
)

func a(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, err
	}
	fmt.Println("parsed", n)
	return n, nil
}

func b(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, err
	}
	fmt.Println("parsed", n)
	return n * 2, nil
}

func main() {
	fmt.Println(a("1"))
	fmt.Println(a("1"))
	fmt.Println(b("2"))
	fmt.Println(b("2"))
}
