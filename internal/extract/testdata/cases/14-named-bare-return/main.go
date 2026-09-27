package main

import (
	"errors"
	"fmt"
)

func parse(s string) (n int, err error) {
	n = len(s)
	if s == "" { //<
		err = errors.New("empty")
		return
	}
	n *= 2
	return //>
}

func main() { fmt.Println(parse("")); fmt.Println(parse("abc")) }
