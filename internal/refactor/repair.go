package refactor

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// A failed build is information, not just a verdict.
//
// Removing the last user of a package leaves its import behind, and Go refuses
// to compile a file with an unused import. That is not the transformation being
// unsafe - it is the transformation being incomplete. Both reverts in an
// earlier run were this, and reading them as "the change broke the build" threw
// away two changes that were fine.
//
// So the loop tries to finish the job before giving up. Only repairs that are
// correct whatever the program meant are attempted: nothing can depend on an
// import that nothing uses.

// BuildProblem is one thing the compiler objected to.
type BuildProblem struct {
	File    string
	Line    int
	Message string
}

// Repairable reports whether a repair exists for this problem.
func (p BuildProblem) Repairable() bool {
	return strings.Contains(p.Message, "imported and not used")
}

// Build compiles the tree and returns what the compiler objected to. An empty
// slice with a nil error means it built.
func Build(dir string) ([]BuildProblem, error) {
	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		return nil, nil
	}
	return func() []BuildProblem {
		var out []BuildProblem
		for _, line := range strings.Split(stderr.String(), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			parts := strings.SplitN(line, ":", 4)
			if len(parts) < 4 {
				continue
			}
			file := parts[0]
			if !filepath.IsAbs(file) {
				file = filepath.Join(dir, file)
			}
			lineNo := 0
			fmt.Sscanf(parts[1], "%d", &lineNo)
			out = append(out, BuildProblem{File: file, Line: lineNo, Message: strings.TrimSpace(parts[3])})
		}
		return out
	}(), nil
}

// Repair attempts to fix what it can and reports whether the tree builds
// afterwards. It is deliberately narrow: a repair that requires guessing what
// the author meant is not a repair, it is another edit, and it would need its
// own measurement and its own gate.
func Repair(dir string) (bool, error) {
	problems, err := Build(dir)
	if err != nil {
		return false, err
	}
	if len(problems) == 0 {
		return true, nil
	}
	files := map[string]bool{}
	for _, p := range problems {
		if !p.Repairable() {
			// One thing that cannot be fixed is enough: repairing the rest
			// would leave a broken tree and a misleading measurement.
			return false, nil
		}
		files[p.File] = true
	}
	var paths []string
	for f := range files {
		paths = append(paths, f)
	}
	sort.Strings(paths)
	for _, f := range paths {
		tidyImports(f)
	}
	after, err := Build(dir)
	if err != nil {
		return false, err
	}
	return len(after) == 0, nil
}

// Snapshot is what each editable file held before a change, by content hash,
// so Format can tell afterwards which files the change wrote.
//
// It used to compare modification times against the moment the change
// started. The kernel stamps files from a coarse clock, so a file written
// just after that moment can carry a time just before it: the loop deleted
// a dead function and left the blank lines around it unformatted.
type Snapshot map[string][sha256.Size]byte

// Stamp records the editable files under dir.
func Stamp(dir string) (Snapshot, error) {
	files, err := editableFilesIn(dir)
	if err != nil {
		return nil, err
	}
	s := Snapshot{}
	for _, path := range files {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		s[path] = sha256.Sum256(b)
	}
	return s, nil
}

// Format puts the tree back into gofmt form.
//
// gopls inlines a call by pasting the body in without re-indenting it, which
// leaves whole functions sitting at the left margin. |AST| cannot see that -
// being independent of formatting is the point of the measure - and the build
// and the tests do not care either, so a change like that passes every gate.
// The only thing that notices is a human, or CI.
//
// Only the files that differ from before are formatted; a nil snapshot
// formats every file.
func Format(dir string, before Snapshot) error {
	files, err := editableFilesIn(dir)
	if err != nil {
		return err
	}
	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// Only what this change touched. Formatting the whole tree
		// rewrites files the change never went near, and the revert
		// restores only the ones it wrote - so a rejected change would
		// still leave those reformatted, with nothing to undo them.
		if h, ok := before[path]; ok && h == sha256.Sum256(src) {
			continue
		}
		// A file that does not parse is not a formatting problem; the build
		// gate is what should report it.
		formatted, err := format.Source(src)
		if err != nil || bytes.Equal(src, formatted) {
			continue
		}
		if err := os.WriteFile(path, formatted, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// tidyImports drops imports nothing uses any more. gopls owns this because
// whether an import is still needed is a question about identifiers and their
// types, not about text.
func tidyImports(path string) {
	goplsPath, ok := Tool("gopls")
	if !ok {
		return
	}
	cmd := exec.Command(goplsPath, "imports", "-w", path)
	cmd.Dir = filepath.Dir(path)
	_ = cmd.Run()
}
