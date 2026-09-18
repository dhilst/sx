//go:build ignore

package template

import "strconv"

func before(n int) string { return strconv.FormatInt(int64(n), 10) }
func after(n int) string  { return strconv.Itoa(n) }
