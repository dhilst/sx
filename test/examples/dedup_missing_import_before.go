//go:build ignore

// The result's type is from a package this file does not import by name, and
// gopls does not add the import.
package main

import (
	"fmt"
	"go/parser"
	"go/token"
)

func a(src string) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "a.go", src, 0)
	fmt.Println(f != nil, err)
}

func b(src string) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "a.go", src, 0)
	fmt.Println(err, f == nil)
}

func main() {
	a("package p")
	a("package p")
	b("package q")
	b("package q")
}
