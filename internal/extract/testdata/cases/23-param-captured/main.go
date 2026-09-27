package main

import "fmt"

func count(words []string, total int) int {
	add := func(n int) { total += n }
	for _, w := range words { //<
		add(len(w))
		total++
	} //>
	add(100)
	return total
}

func main() { fmt.Println(count([]string{"ab", "cde"}, 0)) }
