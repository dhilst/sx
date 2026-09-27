package main

type box struct{ n int }

func fill(b box) int {
	set(b)
	return b.n
}

func set(b box) { b.n = 5 }
