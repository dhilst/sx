package main

import (
	"fmt"
	"os"
)

func run(name string) error {
	if name != "" {
		return newFunction(name)
	}
	return nil
}

func newFunction(name string) error {
	f, err := os.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	fmt.Println("opened")
	if f.Name() == "x" {
		return fmt.Errorf("x")
	}
	fmt.Println("done")
	return nil
}
