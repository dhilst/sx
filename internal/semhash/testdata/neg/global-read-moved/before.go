package main

var counter int

func bump() { counter++ }

func read() int {
	v := counter
	bump()
	return v
}
