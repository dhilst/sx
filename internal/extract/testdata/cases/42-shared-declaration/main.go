package main

import (
	"fmt"
	"sync"
)

func main() {
	var wg sync.WaitGroup
	var pending func() string //<
	var mu sync.Mutex
	wg.Add(1)
	go func() {
		defer wg.Done()
		mu.Lock()
		if pending != nil {
			fmt.Println("worker ran:", pending())
		}
		mu.Unlock()
	}() //>
	mu.Lock()
	pending = func() string { return "set after R" }
	mu.Unlock()
	wg.Wait()
}
