//go:build ignore

package template

import "strings"

func before(s, sub string) bool { return strings.Index(s, sub) == -1 }
func after(s, sub string) bool  { return !strings.Contains(s, sub) }
