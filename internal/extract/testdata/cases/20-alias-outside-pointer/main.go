package main

import "fmt"

type Acc struct{ n int }

func main() {
	var acc Acc
	p := &acc
	acc.n = 5                               //<
	fmt.Println("through p inside R:", p.n) //>
	fmt.Println("after:", acc.n, p.n)
}
