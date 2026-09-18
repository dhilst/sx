//go:build ignore

// For a string, len(s) == 0 is s == "", and len(s) > 0 and len(s) != 0 are
// s != "". A byte slice keeps its len: the templates only match strings.
package main

import "fmt"

func main() {
	name, empty := "x", ""
	data := []byte("y")
	fmt.Println(name == "", empty != "", name != "", len(data) == 0)
}
