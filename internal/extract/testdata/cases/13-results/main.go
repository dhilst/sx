package main

import "fmt"

func main() {
	xs := []int{5, 1, 4}
	lo, hi := xs[0], xs[0]
	for _, x := range xs { //<
		lo = min(lo, x)
		hi = max(hi, x)
	}
	spread := hi - lo //>
	fmt.Println(lo, hi, spread)
}
