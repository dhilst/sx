package main

import (
	"fmt"
	"strconv"
)

func main() {
	n := 1
	p := &n
	m, n := strconv.Itoa(*p), 5 //<
	fmt.Println(m, n)           //>
	fmt.Println(*p)
}
