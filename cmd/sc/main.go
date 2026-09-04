// Command sc scores the structural complexity of Go code.
//
// It charges four things: long functions, many locals, many arguments, and
// deep nesting. Each has a free allowance and grows with the square of the
// overshoot beyond it.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"purgatrix/internal/cost"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "sc:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) > 0 {
		switch args[0] {
		case "refactor":
			return cmdRefactor(args[1:], stdout, stderr)
		case "beam":
			return cmdBeam(args[1:], stdout, stderr)
		}
	}
	fs := flag.NewFlagSet("sc", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", false, "emit JSON")
	limit := fs.Int("n", 20, "how many functions to list (0 for all)")
	tests := fs.Bool("tests", false, "include _test.go files")
	if err := fs.Parse(args); err != nil {
		return err
	}
	targets := fs.Args()
	if len(targets) == 0 {
		targets = []string{"."}
	}

	var report cost.Report
	for _, target := range targets {
		files, err := goFiles(target, *tests)
		if err != nil {
			return err
		}
		for _, f := range files {
			scored, err := cost.ScoreFile(f)
			if err != nil {
				return err
			}
			report.Total += scored.Nodes
			report.Files = append(report.Files, scored)
			report.Functions = append(report.Functions, scored.Functions...)
		}
	}
	cost.Sort(report.Functions)
	if *jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	writeText(stdout, report, *limit)
	return nil
}

func writeText(w io.Writer, report cost.Report, limit int) {
	fmt.Fprintf(w, "%d nodes over %d files, %d functions\n\n", report.Total, len(report.Files), len(report.Functions))
	fmt.Fprintf(w, "%7s  %s\n", "NODES", "FUNCTION")
	for i, f := range report.Functions {
		if limit > 0 && i >= limit {
			fmt.Fprintf(w, "... %d more\n", len(report.Functions)-i)
			break
		}
		fmt.Fprintf(w, "%7d  %s:%d %s\n", f.Nodes, f.File, f.Line, f.Name)
	}
}

// goFiles expands a target into the Go files it covers, skipping what the go
// tool itself ignores.
func goFiles(target string, includeTests bool) ([]string, error) {
	info, err := os.Stat(target)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{target}, nil
	}
	var out []string
	err = filepath.WalkDir(target, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != target && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if !includeTests && strings.HasSuffix(path, "_test.go") {
			return nil
		}
		out = append(out, path)
		return nil
	})
	return out, err
}

func skipDir(name string) bool {
	switch name {
	case "vendor", "testdata":
		return true
	}
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}
