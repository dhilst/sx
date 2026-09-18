//go:build ignore

package template

func before(s string) string { return s[:len(s)] }
func after(s string) string  { return s }
