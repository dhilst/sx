package main

import "fmt"

func f(n int) string {
	c, ok := n*2, n > 0
	if !ok {
		c, ok = 0, true
	}
	return show(c) + fmt.Sprint(ok)
}

func show(c int) string {
	where := func() string { return fmt.Sprint(c) }
	return where()
}
