package main

import (
	"fmt"
	"strconv"
)

func main() {
	var err error
	report := func() { fmt.Println("err is", err) }
	parse := func(s string) int {
		n, err := strconv.Atoi(s) //<
		if err != nil {
			n = -1
		} //>
		return n
	}
	fmt.Println(parse("12"), parse("x"))
	report()
	err = fmt.Errorf("later")
	report()
}
