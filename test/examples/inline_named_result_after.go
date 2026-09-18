//go:build ignore

// A single-return body in expression position whose result type differs from
// the returned expression's type gets an explicit conversion.
package main

import "fmt"

type celsius float64

func main() {
	fmt.Println(celsius(0))
}
