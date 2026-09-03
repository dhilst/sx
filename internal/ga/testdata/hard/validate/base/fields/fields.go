package fields

import "strings"

// HasBlank reports whether a field carries whitespace.
func HasBlank(f string) bool {
	return strings.ContainsAny(f, " \t")
}
