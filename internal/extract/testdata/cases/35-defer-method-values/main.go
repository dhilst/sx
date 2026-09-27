package main

import (
	"fmt"
	"sync"
)

type box struct {
	mu sync.Mutex
	n  int
}

func (b *box) add(k int) int {
	b.mu.Lock() //<
	defer b.mu.Unlock()
	b.n += k //>
	return b.n
}

func main() {
	b := &box{}
	var wg sync.WaitGroup
	for i := 1; i <= 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); b.add(i) }()
	}
	wg.Wait()
	fmt.Println(b.n)
}
