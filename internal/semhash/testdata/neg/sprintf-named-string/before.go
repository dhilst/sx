package main

import "fmt"

type name string

func (n name) String() string { return "<" + string(n) + ">" }

func show(n name) string { return fmt.Sprintf("%s", n) }
