package main

import (
	"fmt"
	"sort"

	"evalparse/config"
)

func main() {
	fmt.Printf("normalize=%q\n", config.Normalize("  spaced  "))
	values, problems := config.Parse([]string{
		"",
		"# comment",
		"host = example.invalid",
		"port=8080",
		"host = other",
		"empty =",
		" = novalue",
		"novalue",
		"=leading",
		"path = /var/log/app.log",
	})
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("value %s=%s\n", k, values[k])
	}
	for _, p := range problems {
		fmt.Printf("problem %s\n", p)
	}
}
