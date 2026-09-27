package main

import "fmt"

func find(xs []int, want int) (int, bool) {
	seen := 0
	for i, x := range xs {
		seen++ //<
		if x == want {
			return i, true
		}
		if x < 0 {
			break
		} //>
	}
	fmt.Println("seen", seen)
	return -1, false
}

func main() {
	fmt.Println(find([]int{4, 5, 6}, 5))
	fmt.Println(find([]int{4, -1, 6}, 6))
	fmt.Println(find([]int{1, 2}, 9))
}
