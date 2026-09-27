package main

type box struct{ n int }

func fill(b box) int {
	b.n = 5
	return b.n
}
