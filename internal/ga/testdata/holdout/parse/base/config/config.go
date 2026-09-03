package config

import "strings"

// Normalize trims surrounding whitespace from a field.
func Normalize(v string) string {
	return strings.TrimSpace(v)
}
