//go:build ignore

// A heavy function with error checks inside the part worth moving out: the
// call site checks the error the new function hands back.
package main

import (
	"fmt"
	"strconv"
	"strings"
)

func parse(input string) (map[string]int, error) {
	out := map[string]int{}
	for _, line := range strings.Split(input, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("no = in %q", line)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		n, err := strconv.Atoi(value)
		if err != nil {
			return nil, err
		}
		if n < 0 {
			return nil, fmt.Errorf("%s is negative", key)
		}
		out[key] = n
	}
	return out, nil
}

func main() {
	m, err := parse("a = 1\n# comment\nb = 2\n")
	fmt.Println(m, err)
	_, err = parse("c = x")
	fmt.Println(err)
}
