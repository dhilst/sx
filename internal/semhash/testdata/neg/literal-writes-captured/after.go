package main

func f() int {
	x := 0
	func() {}()
	return x
}
