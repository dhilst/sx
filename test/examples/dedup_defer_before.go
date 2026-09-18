//go:build ignore

// A defer belongs to the function it is written in. Extracted, the unlock
// would run when the helper returns and the caller would change the counter
// without the lock.
package main

import (
	"fmt"
	"sync"
)

var (
	mu    sync.Mutex
	count int
)

func a(n int) {
	mu.Lock()
	defer mu.Unlock()
	fmt.Println("locked for", n)
	fmt.Println("current", count, n*2)
	count += n
}

func b(n int) {
	mu.Lock()
	defer mu.Unlock()
	fmt.Println("locked for", n)
	fmt.Println("current", count, n*2)
	count -= n
}

func main() {
	a(1)
	a(2)
	b(1)
	b(2)
}
