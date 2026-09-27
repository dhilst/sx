package main

import "fmt"

func main() {
	a, b := 3, 4
	sum := 0
	sum += a * a //<
	sum += b * b
	c := sum * 2 //>
	fmt.Println(sum, c, a, b)
}
