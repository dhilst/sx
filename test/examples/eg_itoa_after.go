//go:build ignore

// fmt.Sprintf("%d", n) and strconv.FormatInt(int64(n), 10) are
// strconv.Itoa(n) for an int. An int64 is left alone.
package main

import (
	"fmt"
	"strconv"
)

func main() {
	n := 42
	var big int64 = 7
	fmt.Println(strconv.Itoa(n), strconv.Itoa(n), fmt.Sprintf("%d", big))
}
