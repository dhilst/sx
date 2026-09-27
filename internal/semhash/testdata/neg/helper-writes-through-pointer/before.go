package main

func setTo(p *int, v int) { *p = v }

func f() int {
	x := 1
	setTo(&x, 2)
	return x
}
