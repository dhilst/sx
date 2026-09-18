//go:build ignore

// The same run, returns included, sits in functions with different result
// types. The return plumbing gopls writes for the first copy does not fit
// the second.
package main

import (
	"errors"
	"fmt"
)

func check(s string) error {
	if s == "" {
		return errors.New("empty input")
	}
	return nil
}

func a(s string) (*int, error) {
	if err := check(s); err != nil {
		return nil, err
	}
	newFunction(s)
	n := len(s)
	return &n, nil
}

func newFunction(s string) {
	fmt.Println("checking", s, len(s))
	fmt.Println("accepted", s)
}

func b(s string) (*string, error) {
	if err := check(s); err != nil {
		return nil, err
	}
	newFunction(s)
	return &s, nil
}

func main() {
	fmt.Println(a("x"))
	fmt.Println(a("y"))
	fmt.Println(b("x"))
	fmt.Println(b("y"))
}
