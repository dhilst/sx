package main

import "fmt"

func check(n int) string {
	s, shouldReturn := newFunction(n)
	_ = shouldReturn
	_ = s
	fmt.Println("fine")
	return "ok"
}

func newFunction(n int) (string, bool) {
	if n < 0 {
		fmt.Println("negative")
		return "bad", true
	}
	return "", false
}
