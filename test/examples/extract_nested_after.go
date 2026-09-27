//go:build ignore

// A heavy function whose weight sits in a loop body two levels down. The body
// is the cut: in a function of its own it weighs its statements at depth one.
package main

import (
	"fmt"
	"strings"
)

type order struct {
	id    string
	items []string
	total int
}

func summarize(orders []order) (int, []string) {
	var lines []string
	sum := 0
	for _, o := range orders {
		if o.total > 0 {
			net, line := newFunction(o)
			lines = append(lines, line)
			sum += net
		}
	}
	fmt.Println(len(lines), "orders")
	return sum, lines
}

func newFunction(o order) (int, string) {
	name := strings.ToUpper(o.id)
	count := len(o.items)
	first := ""
	if count > 0 {
		first = o.items[0]
	}
	tax := o.total / 10
	net := o.total - tax
	line := fmt.Sprintf("%s: %d items, first %q, net %d, tax %d", name, count, first, net, tax)
	return net, line
}

func main() {
	sum, lines := summarize([]order{{"a1", []string{"pen"}, 100}, {"b2", nil, 0}, {"c3", []string{"ink", "cap"}, 55}})
	fmt.Println(sum)
	for _, l := range lines {
		fmt.Println(l)
	}
}
