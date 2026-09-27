package main

func count(xs []int) int {
	n := 0
	m := n
	inc := func() { m++ }
	for range xs {
		inc()
	}
	return n
}
