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
	for i := 0; i < len(values); i++ {
		f := values[i]
		if f != "" {
			if len(f) <= 8 {
				if !HasBlank(f) {
					count++
				} else {
					problems = append(problems, fmt.Sprintf("field %d has whitespace", i))
				}
			} else {
				problems = append(problems, fmt.Sprintf("field %d too long", i))
				if HasBlank(f) {
					problems = append(problems, fmt.Sprintf("field %d has whitespace", i))
				}
			}
		} else {
			problems = append(problems, fmt.Sprintf("field %d empty", i))
		}
	}
	return count, problems
}
