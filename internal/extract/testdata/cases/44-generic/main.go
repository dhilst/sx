package main

import "fmt"

func keysWhere[K comparable, V any](m map[K]V, keep func(V) bool) []K {
	var out []K
	for k, v := range m {
		if !keep(v) { //<
			continue
		}
		out = append(out, k) //>
	}
	return out
}

func main() {
	ks := keysWhere(map[string]int{"a": 1, "b": 20}, func(v int) bool { return v > 10 })
	fmt.Println(ks)
}
