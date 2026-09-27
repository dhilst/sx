package main

import "fmt"

func main() {
	for _, v := range []int{1, 2} {
		switch v {
		case 1:
			fmt.Println("one") //<
			fallthrough        //>
		case 2:
			fmt.Println("one-or-two")
		}
	}
}
