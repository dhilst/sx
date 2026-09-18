//go:build ignore

package template

import (
	"fmt"
	"strconv"
)

func before(n int) string { return fmt.Sprintf("%d", n) }
func after(n int) string  { return strconv.Itoa(n) }
