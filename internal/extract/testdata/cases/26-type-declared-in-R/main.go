package main

import "fmt"

func main() {
	type pair struct{ k, v string } //<
	first := pair{"a", "1"}         //>
	fmt.Println(first, pair{"b", "2"})
}
