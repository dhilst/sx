package main

import "fmt"

func f(n int) string {
	c, ok := n*2, n > 0
	if !ok {
		c, ok = 0, true
	}
	where := func() string { return fmt.Sprint(c) }
	return where() + fmt.Sprint(ok)
}
