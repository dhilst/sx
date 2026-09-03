package config

import (
	"fmt"
	"strings"
)

// Normalize trims surrounding whitespace from a field.
func Normalize(v string) string {
	return strings.TrimSpace(v)
}

// Parse reads key=value lines and reports every problem it finds.
func Parse(lines []string) (map[string]string, []string) {
	out := map[string]string{}
	var problems []string
	for i, line := range lines {
		if line == "" || line[0] == '#' {
			continue
		}
		eq := strings.Index(line, "=")
		if eq <= 0 {
			problems = append(problems, fmt.Sprintf("line %d: missing '='", i+1))
			continue
		}
		key := Normalize(line[:eq])
		val := Normalize(line[eq+1:])
		switch {
		case key == "":
			problems = append(problems, fmt.Sprintf("line %d: empty key", i+1))
		case val == "":
			problems = append(problems, fmt.Sprintf("line %d: empty value for %q", i+1, key))
		case out[key] != "":
			problems = append(problems, fmt.Sprintf("line %d: duplicate key %q", i+1, key))
		default:
			out[key] = val
		}
	}
	return out, problems
}
