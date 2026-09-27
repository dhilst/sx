package main

import "fmt"

func skip(c bool) {
	if c {
		goto done
	}
	fmt.Println("a")
	fmt.Println("b")
done:
	fmt.Println("end")
}
