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
		result, err, shouldReturn := newFunction(line, out)
		if shouldReturn {
			return result, err
		}
	}
	return out, nil
}

func newFunction(line string, out map[string]int) (map[string]int, error, bool) {
	key, value, ok := strings.Cut(line, "=")
	if !ok {
		return nil, fmt.Errorf("no = in %q", line), true
	}
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	n, err := strconv.Atoi(value)
	if err != nil {
		return nil, err, true
	}
	if n < 0 {
		return nil, fmt.Errorf("%s is negative", key), true
	}
	out[key] = n
	return nil, nil, false
}

func main() {
	m, err := parse("a = 1\n# comment\nb = 2\n")
	fmt.Println(m, err)
	_, err = parse("c = x")
	fmt.Println(err)
}
