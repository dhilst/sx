package main

import (
	"fmt"
	"strconv"
)

func parse(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, err
	}
	fmt.Println("parsed", n)
	return n * 2, nil
}
