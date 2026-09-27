//go:build ignore

// A loop whose body weighs more than mB: extract-heavy-block moves the body
// into a function called once per element. Its statements sit two levels
// down in the loop and one level down in the new function.
package main

import (
	"fmt"
	"strings"
)

type entry struct {
	name  string
	tags  []string
	score int
}

func render(entries []entry) []string {
	var out []string
	for _, e := range entries {
		line := newFunction(e)
		out = append(out, line)
	}
	return out
}

func newFunction(e entry) string {
	name := strings.TrimSpace(e.name)
	title := strings.ToUpper(name[:1]) + name[1:]
	tags := strings.Join(e.tags, ", ")
	if tags == "" {
		tags = "none"
	}
	grade := "low"
	if e.score > 50 {
		grade = "mid"
	}
	if e.score > 80 {
		grade = "high"
	}
	line := fmt.Sprintf("%s [%s] %d (%s)", title, tags, e.score, grade)
	return line
}

func main() {
	for _, l := range render([]entry{{" ada", []string{"x"}, 90}, {"bo", nil, 60}, {"cy ", []string{"y", "z"}, 10}}) {
		fmt.Println(l)
	}
}
