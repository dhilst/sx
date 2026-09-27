package main

func f() int {
	x := 0
	var fs []func() int
	for i := 0; i < 2; i++ {
		x = i
		fs = append(fs, func() int { return x })
	}
	return fs[0]()
}
