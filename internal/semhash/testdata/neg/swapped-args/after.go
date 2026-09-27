package main

import "fmt"

func sub(a, b int) int { return a - b }

func main() { fmt.Println(sub(3, 1), sub(1, 3)) }

func run(x, y int) int { return helper(y, x) }

func helper(a, b int) int { return sub(a, b) }
