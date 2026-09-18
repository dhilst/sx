//go:build ignore

// An untyped constant passed for a typed parameter keeps its type when it is
// substituted.
package main

import (
	"fmt"
	"time"
)

func main() {
	fmt.Println("waiting", time.Duration(5))
	fmt.Println("waited", time.Duration(5))
}
