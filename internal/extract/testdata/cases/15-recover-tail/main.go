package main

import "fmt"

func safeDiv(a, b int) (q int, ok bool) {
	ok = true
	defer func() { //<
		if recover() != nil {
			ok = false
		}
	}()
	q = a / b
	return //>
}

func main() { fmt.Println(safeDiv(6, 3)); fmt.Println(safeDiv(1, 0)) }
