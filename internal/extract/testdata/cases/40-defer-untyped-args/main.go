package main

import "fmt"

func scale(f float32, err error, names ...string) { fmt.Println(f*2, err, names) }

func main() {
	defer scale(1.5, nil)         //<
	defer scale(2, nil, "a", "b") //>
	fmt.Println("body")
}
