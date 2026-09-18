//go:build ignore

package template

import "bytes"

func before(a, b []byte) bool { return bytes.Compare(a, b) == 0 }
func after(a, b []byte) bool  { return bytes.Equal(a, b) }
