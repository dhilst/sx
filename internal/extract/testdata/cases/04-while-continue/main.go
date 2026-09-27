package main

import "fmt"

func main() {
	n, odd := 20, 0
	for n > 0 {
		n--
		if n%2 == 0 { //<
			continue
		}
		odd += n //>
	}
	fmt.Println(odd)
}
