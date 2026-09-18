//go:build ignore

// fmt.Errorf("%s", s) is errors.New(s): the errors import arrives, and fmt
// goes when nothing else uses it.
package main

import (
	"errors"
)

func main() {
	println(errors.New("x") != nil)
}
