//go:build ignore

// Results of struct type have T{} as their zero value.
package main

import "fmt"

type point struct{ x, y int }

func a(n int) (point, error) {
	if n < 0 {
		return point{}, fmt.Errorf("negative %d", n)
	}
	fmt.Println("valid", n)
	fmt.Println("scaled", n*2)
	return point{n, n}, nil
}

func b(n int) (point, error) {
	if n < 0 {
		return point{}, fmt.Errorf("negative %d", n)
	}
	fmt.Println("valid", n)
	fmt.Println("scaled", n*2)
	return point{n, -n}, nil
}

func main() {
	fmt.Println(a(1))
	fmt.Println(a(1))
	fmt.Println(b(2))
	fmt.Println(b(2))
}
