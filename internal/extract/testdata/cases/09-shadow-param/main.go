package main

import "fmt"

func run(args []string) error {
	if len(args) > 0 && args[0] == "sub" {
		var args []string = args[1:] //<
		for _, a := range args {
			fmt.Println("arg", a)
		}
		return nil //>
	}
	fmt.Println("default", len(args))
	return nil
}

func main() { run([]string{"sub", "x", "y"}); run([]string{"z"}) }
