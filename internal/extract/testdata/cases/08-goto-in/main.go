package main

import "fmt"

func main() {
	n := 0
	if n == 0 {
		goto done
	}
	n = 100
done: //<
	n += 7 //>
	fmt.Println(n)
}
