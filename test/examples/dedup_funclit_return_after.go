//go:build ignore

// Returns inside a function literal take the literal's signature, not the
// enclosing function's.
package main

import "fmt"

func a() func(int) (string, bool) {
	return func(n int) (string, bool) {
		if n > 3 {
			return "big", true
		}
		fmt.Println("small", n)
		return "", false
	}
}

func b() func(int) (string, bool) {
	return func(n int) (string, bool) {
		if n > 3 {
			return "big", true
		}
		fmt.Println("small", n)
		return "small", false
	}
}

func main() {
	fmt.Println(a()(1))
	fmt.Println(a()(1))
	fmt.Println(b()(5))
	fmt.Println(b()(5))
}
