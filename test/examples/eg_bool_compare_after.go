//go:build ignore

// x == true is x, x != false is x: several templates match one file.
package main

import "fmt"

func main() {
	ready, done := true, false
	if ready {
		fmt.Println("ready")
	}
	if done {
		fmt.Println("done")
	}
}
