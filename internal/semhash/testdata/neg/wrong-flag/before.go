package main

import "fmt"

func check(n int) string {
	if n < 0 {
		return "bad"
	}
	fmt.Println("fine")
	return "ok"
}
