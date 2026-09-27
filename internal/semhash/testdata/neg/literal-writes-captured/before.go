package main

func f() int {
	x := 0
	func() { x = 5 }()
	return x
}
