package main

func area(w, h int) int {
	a := w * h
	if a > 100 {
		return 100
	}
	return a
}
