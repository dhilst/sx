package main

import "fmt"

func main() {
	out := []string{}
	for _, v := range []int{1, 2, 3} {
		switch v {
		case 2:
			out = append(out, "two") //<
			if len(out) > 0 {
				break
			}
			out = append(out, "never") //>
		default:
			out = append(out, fmt.Sprint(v))
		}
	}
	fmt.Println(out)
}
