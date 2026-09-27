package main

import "fmt"

func main() {
	for i := 0; i < 4; i++ {
		if i == 1 { //<
			continue
		}
		msg := fmt.Sprint("item ", i) //>
		fmt.Println(msg)
	}
}
