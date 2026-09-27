//go:build ignore

// A long function with no nesting to speak of. Its weight is its length, and
// it is split into runs of top-level statements.
package main

import (
	"fmt"
	"strings"
)

func report(name string, scores []int) string {
	var lines []string
	lines = append(lines, "report for "+name)
	lines = append(lines, strings.Repeat("=", 20))
	n, lines, mean := newFunction(scores, lines)
	lines = append(lines, strings.Repeat("-", 20))
	above := 0
	for _, s := range scores {
		if s > mean {
			above++
		}
	}
	lines = append(lines, fmt.Sprintf("above mean: %d", above))
	below := n - above
	lines = append(lines, fmt.Sprintf("at or below: %d", below))
	lines = append(lines, strings.Repeat("=", 20))
	lines = append(lines, "end of report for "+name)
	return strings.Join(lines, "\n") + "\n"
}

func newFunction(scores []int, lines []string) (int, []string, int) {
	n := len(scores)
	lines = append(lines, fmt.Sprintf("count: %d", n))
	lo, hi, sum := scores[0], scores[0], 0
	for _, s := range scores {
		sum += s
		lo = min(lo, s)
		hi = max(hi, s)
	}
	lines = append(lines, fmt.Sprintf("sum: %d", sum))
	lines = append(lines, fmt.Sprintf("low: %d", lo))
	lines = append(lines, fmt.Sprintf("high: %d", hi))
	mean := sum / n
	lines = append(lines, fmt.Sprintf("mean: %d", mean))
	spread := hi - lo
	lines = append(lines, fmt.Sprintf("spread: %d", spread))
	return n, lines, mean
}

func main() {
	fmt.Print(report("team", []int{3, 9, 4, 7, 1, 8}))
}
