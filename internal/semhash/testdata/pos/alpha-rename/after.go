package main

func area(width, height int) int {
	size := width * height
	if size > 100 {
		return 100
	}
	return size
}
