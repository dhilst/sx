package main

var counter int

func bump() { counter++ }

func read() int {
	bump()
	v := counter
	return v
}
