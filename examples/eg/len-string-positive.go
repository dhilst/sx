//go:build ignore

package template

func before(s string) bool { return len(s) > 0 }
func after(s string) bool  { return s != "" }
