package main

import "fmt"

func retry() int {
	attempts := 0
again:
	attempts++
	fmt.Println("attempt", attempts)
	if attempts < 3 { //<
		fmt.Println("retrying")
		goto again
	} //>
	return attempts
}

func main() { fmt.Println(retry()) }
