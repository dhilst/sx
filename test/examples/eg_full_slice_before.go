//go:build ignore

// s[:len(s)] is s. The template drops the second copy of the wildcard, so the
// saving grows with what it binds.
package main

import "fmt"

type box struct{ name string }

func main() {
	b := box{"hello"}
	fmt.Println(b.name[:len(b.name)])
}
