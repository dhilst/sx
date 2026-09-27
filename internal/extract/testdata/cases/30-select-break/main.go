package main

import "fmt"

func main() {
	ch := make(chan int, 3)
	ch <- 1
	ch <- 2
	close(ch)
	got := 0
	for {
		v, ok := <-ch //<
		if !ok {
			break
		}
		got += v //>
	}
	fmt.Println(got)
}
