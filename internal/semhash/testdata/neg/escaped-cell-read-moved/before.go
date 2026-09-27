package main

func set(p *int) { *p = 5 }

func read() int {
	x := 1
	p := &x
	set(p)
	return x
}
