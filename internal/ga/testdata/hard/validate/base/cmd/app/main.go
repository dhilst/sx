package main

import (
	"fmt"

	"evalvalidate/fields"
)

func main() {
	for _, f := range []string{"ok", "a b"} {
		fmt.Printf("blank(%q)=%v\n", f, fields.HasBlank(f))
	}
}
