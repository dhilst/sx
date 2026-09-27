package main

import (
	"fmt"
	"strconv"
)

type count int

func (c count) Format(f fmt.State, verb rune) { f.Write([]byte("many")) }

func show(c count) string { return fmt.Sprintf("%d", c) }

var _, _ = fmt.Sprintf, strconv.Itoa
