//go:build ignore

// Two runs that differ only in an operator are not copies: one adds, the
// other subtracts.
package main

import "fmt"

var count int

func a(n int) {
	newFunction(n)
	count += n
}

func newFunction(n int) {
	fmt.Println("updating by", n)
	fmt.Println("current", count, n*2)
}

func b(n int) {
	newFunction(n)
	count -= n
}

func main() {
	a(1)
	a(2)
	b(1)
	b(2)
	fmt.Println(count)
}
