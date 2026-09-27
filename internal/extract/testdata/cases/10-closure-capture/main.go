package main

import "fmt"

func main() {
	count := 0
	inc := func() { count++ } //<
	inc()                     //>
	inc()
	count += 10
	fmt.Println(count)
}
