//go:build ignore

// A command whose subcommand is written inline in run: a branch heavier than
// mB that closes its file on the way out. The search refuses every cut that
// holds the defer, since a defer moved into a helper runs when the helper
// returns. This branch ends by returning from run, so the call becomes
// "return f(...)" and the defer still runs as run returns: extract-if-block
// moves it, defer and all.
package main

import (
	"fmt"
	"os"
	"strings"
)

func run(args []string) error {
	if len(args) > 0 && args[0] == "count" {
		f, err := os.Open(args[1])
		if err != nil {
			return err
		}
		defer f.Close()
		return newFunction(f)
	}
	fmt.Println("usage: count FILE")
	return nil
}

func newFunction(f *os.File) error {
	buf := make([]byte, 1<<16)
	n, err := f.Read(buf)
	if err != nil {
		return err
	}
	text := string(buf[:n])
	lines := strings.Count(text, "\n")
	words := len(strings.Fields(text))
	upper := 0
	for _, r := range text {
		if r >= 'A' && r <= 'Z' {
			upper++
		}
	}
	fmt.Println("lines:", lines)
	fmt.Println("words:", words)
	fmt.Println("upper:", upper)
	if words > 0 {
		fmt.Println("per line:", words/max(1, lines))
	}
	return nil
}

func main() {
	os.WriteFile("/tmp/sx-heuristic-example.txt", []byte("One Two\nthree\n"), 0o644)
	fmt.Println(run([]string{"count", "/tmp/sx-heuristic-example.txt"}))
	fmt.Println(run(nil))
}
