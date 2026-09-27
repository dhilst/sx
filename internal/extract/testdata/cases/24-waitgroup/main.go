package main

import (
	"fmt"
	"sync"
)

func main() {
	var wg sync.WaitGroup
	var mu sync.Mutex
	results := map[int]int{}
	for i := 0; i < 4; i++ { //<
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			mu.Lock()
			results[i] = i * i
			mu.Unlock()
		}(i)
	} //>
	wg.Wait()
	fmt.Println(len(results), results[3])
}
