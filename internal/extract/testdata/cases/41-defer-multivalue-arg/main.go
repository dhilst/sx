package main

import "fmt"

func pair() (int, string)  { return 7, "seven" }
func show(n int, s string) { fmt.Println(n, s) }

func main() {
	defer show(pair())  //<
	fmt.Println("body") //>
	fmt.Println("end")
}
