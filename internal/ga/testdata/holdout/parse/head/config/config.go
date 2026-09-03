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
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if len(line) > 0 {
			if line[0] != '#' {
				eq := -1
				for j := 0; j < len(line); j++ {
					if line[j] == '=' {
						if eq == -1 {
							eq = j
						}
					}
				}
				if eq > 0 {
					key := Normalize(line[:eq])
					val := Normalize(line[eq+1:])
					if key != "" {
						if val != "" {
							if _, exists := out[key]; exists {
								problems = append(problems, fmt.Sprintf("line %d: duplicate key %q", i+1, key))
							} else {
								out[key] = val
							}
						} else {
							problems = append(problems, fmt.Sprintf("line %d: empty value for %q", i+1, key))
						}
					} else {
						problems = append(problems, fmt.Sprintf("line %d: empty key", i+1))
					}
				} else {
					problems = append(problems, fmt.Sprintf("line %d: missing '='", i+1))
				}
			}
		}
	}
	return out, problems
}
