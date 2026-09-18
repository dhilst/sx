package refactor

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
)

// What a change can break is not what a change touches.
//
// The loop was given a directory and tested that directory. Refactoring
// internal/runtime/maps passed its own tests - which are good enough to reject
// six other inlines in the same run - and left reflect corrupting type
// descriptors, because reflect imports it and nothing ran reflect. The whole
// standard library built and every package gate was green.
//
// So the gate is the changed package and everything that imports it. That set
// is a question about the module, and go list answers it.

// Scope is the set of packages a change to one package must be tested against.
type Scope struct {
	root string   // module directory the tests are run from
	pkgs []string // import paths: the package itself, then its importers
}

// TestScope works out what has to pass for a change under dir to be accepted.
// A tree with no module, or a go list that will not answer, gives a scope of
// dir itself - the old behaviour, which is right for a whole repository and
// wrong only for a part of one.
func TestScope(dir string) Scope {
	root, err := goList(dir, "-m", "-f", "{{.Dir}}")
	if err != nil || root == "" {
		return Scope{root: dir, pkgs: []string{"./..."}}
	}
	self, err := goList(dir, "-f", "{{.ImportPath}}")
	if err != nil || self == "" {
		return Scope{root: dir, pkgs: []string{"./..."}}
	}

	out, err := func() (string, error) {
		cmd := exec.Command("go", "list", "-e", "-f", "{{.ImportPath}} {{join .Deps \" \"}}", "./...")
		cmd.Dir = root
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			return "", err
		}
		return out.String(), nil
	}()
	if err != nil {
		return Scope{root: dir, pkgs: []string{"./..."}}
	}
	pkgs := []string{self}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] == self {
			continue
		}
		for _, dep := range fields[1:] {
			if dep == self {
				pkgs = append(pkgs, fields[0])
				break
			}
		}
	}
	return Scope{root: root, pkgs: pkgs}
}

// Packages is what the scope will test.
func (s Scope) Packages() []string { return s.pkgs }

// Test runs the tests of every package in the scope.
func (s Scope) Test() error {
	args := append([]string{"test"}, s.pkgs...)
	cmd := exec.Command("go", args...)
	cmd.Dir = s.root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if line := firstLine(stderr.String()); line != "" {
			return errors.New(line)
		}
		return err
	}
	return nil
}

func goList(dir string, args ...string) (string, error) {
	cmd := exec.Command("go", append([]string{"list"}, args...)...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}
