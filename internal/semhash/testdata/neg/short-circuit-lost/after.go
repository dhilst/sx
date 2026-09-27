package main

func ok(p *int) bool {
	v := *p > 0
	return p != nil && v
}
