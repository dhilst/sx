//go:build ignore

// A nested return inside the part worth moving out hands back total, a
// variable of the enclosing function. gopls would carry it back under the
// same name with :=, reuse total at the call, and overwrite it with zero on
// the path that does not return. The model refuses the cut.
package main

import (
	"errors"
	"fmt"
	"strings"
)

func tally(words []string) (int, error) {
	total := 0
	seen := map[string]bool{}
	for _, w := range words {
		w = strings.TrimSpace(w)
		if w == "" {
			continue
		}
		if w == "stop" {
			return total, errors.New("stopped")
		}
		lower := strings.ToLower(w)
		if seen[lower] {
			continue
		}
		total = newFunction(seen, lower, total)
	}
	return total, nil
}

func newFunction(seen map[string]bool, lower string, total int) int {
	seen[lower] = true
	n := len(lower)
	if strings.HasSuffix(lower, "s") {
		n--
	}
	if n > 3 {
		n = 3
	}
	total += n
	return total
}

func main() {
	fmt.Println(tally([]string{"a", "B", "b", " cc "}))
	fmt.Println(tally([]string{"x", "stop", "y"}))
}
