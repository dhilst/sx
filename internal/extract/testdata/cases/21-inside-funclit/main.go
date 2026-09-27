package main

import "fmt"

func main() {
	label, err := func() (string, error) {
		n := 3
		if n > 2 { //<
			return "big", nil
		}
		return "small", fmt.Errorf("too small") //>
	}()
	fmt.Println(label, err)
}
