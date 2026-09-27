package main

import (
	"fmt"
	"strconv"
)

func parse(s string) (int, error) {
	n, err := newFunction(s)
	if err != nil {
		return 0, err
	}
	fmt.Println("parsed", n)
	return n * 2, nil
}

func newFunction(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, err
	}
	return n, nil
}
