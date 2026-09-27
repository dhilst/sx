package main

type T struct{}

func use(t T) T { return t }

func (T) String() string { return "T" }
