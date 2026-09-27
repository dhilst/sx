//go:build ignore

// printCard is called once, and inlining it would save its declaration and
// the call: |AST| alone takes that. But its caller is already heavy, and
// taking printCard's weight into it, a level down, costs more reading than
// the nodes save, so J leaves it where it is.
package main

import (
	"fmt"
	"strings"
)

func printCard(title string, age int) {
	fmt.Println(strings.Repeat("-", 20))
	fmt.Println("name:", title)
	fmt.Println("age:", age)
	fmt.Println("decade:", age/10*10)
	fmt.Println("initial:", title[:1])
	fmt.Println("length:", len(title))
	fmt.Println(strings.Repeat("-", 20))
}

func register(names []string, ages []int) []string {
	var out []string
	for i, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		age := ages[i]
		group := "adult"
		switch {
		case age < 18:
			group = "minor"
		case age > 65:
			group = "senior"
		}
		title := strings.ToUpper(name[:1]) + name[1:] + " (" + group + ")"
		printCard(title, age)
		out = append(out, title)
	}
	return out
}

func main() {
	fmt.Println(register([]string{" ada", "", "cy "}, []int{36, 5, 70}))
}
