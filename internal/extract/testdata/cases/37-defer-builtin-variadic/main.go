package main

import "fmt"

func show(prefix string, xs ...int) { fmt.Println(prefix, xs) }

func main() {
	ch := make(chan int, 1)
	xs := []int{1, 2}
	defer show("variadic:", xs...) //<
	defer close(ch)                //>
	xs[0] = 99
	ch <- 7
	fmt.Println(<-ch)
}
