package main

import "fmt"

func main() {
	type point struct{ x, y int }
	pts := []point{{1, 2}, {3, 4}}
	sum := point{} //<
	for _, p := range pts {
		sum.x += p.x
		sum.y += p.y
	} //>
	fmt.Println(sum, point{9, 9})
}
