package main

import "fmt"

func main() {
	total := 0
	for i := 0; i < 10; i++ {
		if i == 7 { //<
			break
		}
		total += i //>
	}
	fmt.Println(total)
}
