package main

func f(n int) int {
	x := 0
	p := &x
	for i := 0; i < n; i++ {
	}
	_ = p
	return x
}
