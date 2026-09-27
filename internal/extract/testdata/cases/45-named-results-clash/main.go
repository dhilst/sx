package main

import (
	"errors"
	"fmt"
)

func check(path string) (ok bool, err error) {
	if err := validate(path); err != nil {
		ok = false                                       //<
		return ok, fmt.Errorf("check %s: %w", path, err) //>
	}
	return true, nil
}

func validate(p string) error {
	if p == "" {
		return errors.New("empty")
	}
	return nil
}

func main() { fmt.Println(check("x")); fmt.Println(check("")) }
