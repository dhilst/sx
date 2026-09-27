package main

import "fmt"

func check(n int) (string, error) {
	s, err, shouldReturn := newFunction(n)
	if shouldReturn {
		return s, err
	}
	fmt.Println("fine")
	return "ok", nil
}

func newFunction(n int) (string, error, bool) {
	fmt.Println("checking")
	if n < 0 {
		fmt.Println("negative")
		return "", fmt.Errorf("negative %d", n), true
	}
	return "", nil, false
}
