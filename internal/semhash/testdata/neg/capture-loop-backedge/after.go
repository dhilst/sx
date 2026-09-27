package main

func f() int {
	x := 0
	var fs []func() int
	for i := 0; i < 2; i++ {
		x = i
		y := x
		fs = append(fs, func() int { return y })
	}
	return fs[0]()
}
