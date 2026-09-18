//go:build ignore

// An argument with effects, referenced once, is still substituted.
package main

import "fmt"

func get() int { return 3 }

func show(a int) {
	fmt.Println("value", a)
	fmt.Println("done")
}

func main() {
	show(get())
}
