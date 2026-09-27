package main

func count(xs []int) int {
	n := 0
	inc := func() { n++ }
	for range xs {
		inc()
	}
	return n
}
