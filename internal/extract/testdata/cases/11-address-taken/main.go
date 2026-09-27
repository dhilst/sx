package main

import "fmt"

type Acc struct{ n int }

func (a *Acc) Add(k int) { a.n += k }

func main() {
	var acc Acc
	p := &acc
	acc.Add(1) //<
	acc.Add(2) //>
	p.Add(3)
	fmt.Println(acc.n)
}
