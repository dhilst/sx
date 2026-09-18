//go:build ignore

package template

import "fmt"

func before(s string) string { return fmt.Sprintf("%s", s) }
func after(s string) string  { return s }
