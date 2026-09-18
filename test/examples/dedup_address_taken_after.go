//go:build ignore

// A result whose address is taken in the run must not be returned by value:
// the caller would get a copy while cmd writes to the helper's local.
package main

import (
	"bytes"
	"fmt"
	"os/exec"
)

func a() string {
	cmd := exec.Command("true")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	cmd.Run()
	return out.String()
}

func b() string {
	cmd := exec.Command("false")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	cmd.Run()
	return out.String() + "!"
}

func main() {
	fmt.Println(a(), b())
	fmt.Println(a(), b())
}
