//go:build ignore

// fmt.Sprintf("%s", v) is v only for a string: an error argument does not
// match the template.
package main

import (
	"errors"
	"fmt"
)

func main() {
	name := "x"
	err := errors.New("e")
	fmt.Println(fmt.Sprintf("%s", name), fmt.Sprintf("%s", err))
}
