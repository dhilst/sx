package main

import (
	"fmt"
	"strconv"
)

func parse(s string) (int, error) { return strconv.Atoi(s) }

func firstNumber(words []string) (int, error) {
	for _, w := range words {
		if w == "" { //<
			continue
		}
		if w[0] >= '0' && w[0] <= '9' {
			return parse(w)
		} //>
	}
	return 0, fmt.Errorf("none")
}

func main() {
	fmt.Println(firstNumber([]string{"", "x", "42", "7"}))
	fmt.Println(firstNumber([]string{"a"}))
}
