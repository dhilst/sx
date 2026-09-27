package main

import "fmt"

func work(log *[]string) int {
	*log = append(*log, "start")
	defer func() { *log = append(*log, "deferred") }() //<
	*log = append(*log, "middle")                      //>
	*log = append(*log, "end")
	return len(*log)
}

func main() {
	var log []string
	n := work(&log)
	fmt.Println(n, log)
}
