package main

import "fmt"

func skip(c bool) {
	if c {
		goto done
	}
	fmt.Println("a")
done:
	fmt.Println("b")
	fmt.Println("end")
}
