package main

import "fmt"

func main() {
	i := 0
loop:
	i++ //<
	if i < 5 {
		goto loop
	} //>
	fmt.Println(i)
}
