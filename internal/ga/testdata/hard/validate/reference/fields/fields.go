package fields

import (
	"fmt"
	"strings"
)

// HasBlank reports whether a field carries whitespace.
func HasBlank(f string) bool {
	return strings.ContainsAny(f, " \t")
}

// Validate counts acceptable fields and reports every problem it finds.
// An over-long field is reported for its length and, separately, for any
// whitespace it carries.
func Validate(values []string) (int, []string) {
	count := 0
	var problems []string
	for i, f := range values {
		switch {
		case f == "":
			problems = append(problems, fmt.Sprintf("field %d empty", i))
		case len(f) > 8:
			problems = append(problems, fmt.Sprintf("field %d too long", i))
			if HasBlank(f) {
				problems = append(problems, fmt.Sprintf("field %d has whitespace", i))
			}
		case HasBlank(f):
			problems = append(problems, fmt.Sprintf("field %d has whitespace", i))
		default:
			count++
		}
	}
	return count, problems
}
