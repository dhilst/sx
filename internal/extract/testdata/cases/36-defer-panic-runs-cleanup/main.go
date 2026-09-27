package main

import (
	"errors"
	"fmt"
)

func step(log *[]string, fail bool) {
	*log = append(*log, "open") //<
	defer func() { *log = append(*log, "close") }()
	if fail {
		panic(errors.New("failed"))
	} //>
	*log = append(*log, "done")
}

func main() {
	var log []string
	func() {
		defer func() { recover() }()
		step(&log, false)
		step(&log, true)
	}()
	fmt.Println(log)
}
