package main

import "fmt"

func main() {
	pairs := 0
outer:
	for i := 0; i < 5; i++ {
		for j := 0; j < 5; j++ {
			if j > i { //<
				continue outer
			}
			pairs++ //>
		}
	}
	fmt.Println(pairs)
}
