package main

import "fmt"

func classify(n int) string {
	label := "small"
	if n < 0 { //<
		return "negative"
	}
	if n > 100 {
		label = "big"
	} //>
	label += "!"
	return label
}

func main() { fmt.Println(classify(-1), classify(5), classify(500)) }
