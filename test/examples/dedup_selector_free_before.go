//go:build ignore

// A free variable reached through a selector is one parameter, not one per
// field.
package main

import "fmt"

type config struct {
	name string
	port int
}

func a(c config) {
	fmt.Println("name", c.name)
	fmt.Println("port", c.port+1)
}

func b(c config) {
	fmt.Println("name", c.name)
	fmt.Println("port", c.port+1)
}

func main() {
	a(config{"x", 1})
	a(config{"x", 1})
	b(config{"y", 2})
	b(config{"y", 2})
}
