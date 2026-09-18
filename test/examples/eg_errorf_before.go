//go:build ignore

// fmt.Errorf("%s", s) is errors.New(s): the errors import arrives, and fmt
// goes when nothing else uses it.
package main

import "fmt"

func check(s string) error {
	return fmt.Errorf("%s", s)
}

func main() {
	println(check("x") != nil)
}
