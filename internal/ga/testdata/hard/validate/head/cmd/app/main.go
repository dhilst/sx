package main

import (
	"fmt"

	"evalvalidate/fields"
)

func main() {
	for _, f := range []string{"ok", "a b"} {
		fmt.Printf("blank(%q)=%v\n", f, fields.HasBlank(f))
	}
	count, problems := fields.Validate([]string{
		"ok", "", "a b", "waytoolongvalue", "long value here", "\tlead", "exactly8", "exactly88",
	})
	fmt.Printf("count=%d\n", count)
	for _, p := range problems {
		fmt.Printf("problem %s\n", p)
	}
}
