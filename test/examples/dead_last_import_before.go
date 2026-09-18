//go:build ignore

// The unreachable function uses the file's only import, so the whole import
// declaration goes.
package main

import "strings"

func shout(s string) string {
	return strings.ToUpper(s)
}

func main() {
	println("hello")
}
