package main

import "fmt"

func check(n int) string {
	s, shouldReturn := newFunction(n)
	if !shouldReturn {
		return s
	}
	fmt.Println("fine")
	return "ok"
}

func newFunction(n int) (string, bool) {
	if n < 0 {
		return "bad", true
	}
	return "", false
}
