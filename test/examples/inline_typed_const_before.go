//go:build ignore

// An untyped constant passed for a typed parameter keeps its type when it is
// substituted.
package main

import (
	"fmt"
	"time"
)

func wait(d time.Duration) {
	fmt.Println("waiting", d)
	fmt.Println("waited", d)
}

func main() {
	wait(5)
}
