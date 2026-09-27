//go:build ignore

// A nested return that is not an error check needs a shouldReturn flag.
package main

import "fmt"

func a(n int) string {
	if n < 0 {
		return "negative"
	}
	newFunction(n)
	return "ok"
}

func newFunction(n int) {
	fmt.Println("checked", n)
	fmt.Println("positive", n > 0)
}

func b(n int) string {
	if n < 0 {
		return "negative"
	}
	newFunction(n)
	return "fine"
}

func main() {
	fmt.Println(a(1), b(-1))
	fmt.Println(a(1), b(-1))
}
