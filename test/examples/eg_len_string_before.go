//go:build ignore

// For a string, len(s) == 0 is s == "", and len(s) > 0 and len(s) != 0 are
// s != "". A byte slice keeps its len: the templates only match strings.
package main

import "fmt"

func main() {
	name, empty := "x", ""
	data := []byte("y")
	fmt.Println(len(name) == 0, len(empty) > 0, len(name) != 0, len(data) == 0)
}
