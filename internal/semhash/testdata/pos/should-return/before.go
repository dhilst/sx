package main

import "fmt"

func check(n int) (string, error) {
	fmt.Println("checking")
	if n < 0 {
		fmt.Println("negative")
		return "", fmt.Errorf("negative %d", n)
	}
	fmt.Println("fine")
	return "ok", nil
}
