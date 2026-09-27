package main

import "fmt"

func risky(n int) (out string) {
	out = "ok"
	defer func() { //<
		if r := recover(); r != nil {
			out = fmt.Sprint("recovered: ", r)
		}
	}() //>
	if n == 0 {
		panic("zero")
	}
	return out
}

func main() { fmt.Println(risky(1)); fmt.Println(risky(0)) }
