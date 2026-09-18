package refactor

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
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

// Scope is the set of packages a change under one directory must be tested
// against.
type Scope struct {
	root    string   // module directory the tests are run from
	pkgs    []string // import paths: the packages under the directory, then their importers
	targets int      // how many of pkgs are under the directory
}

// TestScope works out what has to pass for a change under dir to be accepted:
// every package under dir, and every package in the module that depends on
// one of them. Pointed at the module root, that is ./... .
//
// It used to take the one package in dir. A module whose root directory is
// itself a package - cc-connect has a main.go at the top - then got a scope of
// that package and its importers, and every change in core/ or platform/ was
// accepted without their tests running.
func TestScope(dir string) Scope {
	whole := Scope{root: dir, pkgs: []string{"./..."}}
	root, err := goList(dir, "-m", "-f", "{{.Dir}}")
	if err != nil || root == "" || filepath.Clean(root) == filepath.Clean(dir) {
		return whole
	}
	under, err := goList(dir, "-e", "-f", "{{.ImportPath}}", "./...")
	if err != nil || under == "" {
		return whole
	}
	targets := map[string]bool{}
	var pkgs []string
	for _, p := range strings.Fields(under) {
		targets[p] = true
		pkgs = append(pkgs, p)
	}
	out, err := goList(root, "-e", "-f", "{{.ImportPath}} {{join .Deps \" \"}}", "./...")
	if err != nil {
		return whole
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || targets[fields[0]] {
			continue
		}
		for _, dep := range fields[1:] {
			if targets[dep] {
				pkgs = append(pkgs, fields[0])
				break
			}
		}
	}
	return Scope{root: root, pkgs: pkgs, targets: len(targets)}
}

// Without is the scope less one package. A scope of ./... is spelled out
// first, since go test cannot exclude a package from a pattern.
func (s Scope) Without(pkg string) Scope {
	pkgs := s.pkgs
	if len(pkgs) == 1 && pkgs[0] == "./..." {
		if out, err := goList(s.root, "-e", "-f", "{{.ImportPath}}", "./..."); err == nil {
			pkgs = strings.Fields(out)
		}
	}
	var kept []string
	targets := s.targets
	for i, p := range pkgs {
		if p == pkg {
			if i < s.targets {
				targets--
			}
			continue
		}
		kept = append(kept, p)
	}
	return Scope{root: s.root, pkgs: kept, targets: targets}
}

// Targets is how many of the scope's packages are under the directory; the
// rest import them.
func (s Scope) Targets() int { return s.targets }

// Packages is what the scope will test.
func (s Scope) Packages() []string { return s.pkgs }

// Test runs the tests of every package in the scope.
func (s Scope) Test() error {
	failures, err := s.Failures(nil)
	if err != nil {
		return err
	}
	return failures.Since(nil)
}

// running is the test runs in progress, by process group.
var running sync.Map

// StopTests kills every test run in progress, and whatever the tests
// started. Interrupting sx used to leave them behind: flowstate's suite
// starts a headless browser and a workflow server, and both outlived it.
func StopTests() {
	running.Range(func(pid, _ any) bool {
		killGroup(pid.(int))
		return true
	})
}

// Failures is what failed in a test run: "package\x00Test" for a test, and
// "package\x00" for a package that failed without a failing test - a build
// error, a panic, a timeout.
type Failures map[string]bool

// Failures runs the scope's tests, skipping the ones in skip, and reports what
// failed.
//
// A tree rarely starts with every test passing. flowstate has six tests that
// drive a headless browser and time out without one; with the gate asking only
// "did go test pass", every change was rejected before it was looked at, and
// each rejection waited out six minute-long timeouts. So the gate records what
// fails before the first change, skips it, and rejects a change only for a
// failure it caused.
func (s Scope) Failures(skip Failures) (Failures, error) {
	return s.run(skip, false)
}

// Recheck runs the scope's tests again without Go's test cache. Asked of an
// unchanged tree, the cache answers with the result recorded before, which
// is the one question a recheck must not have answered for it.
func (s Scope) Recheck(skip Failures) (Failures, error) {
	return s.run(skip, true)
}

func (s Scope) run(skip Failures, fresh bool) (Failures, error) {
	args := []string{"test", "-json"}
	if fresh {
		args = append(args, "-count=1")
	}
	var names []string
	seen := map[string]bool{}
	for k := range skip {
		if _, test, _ := strings.Cut(k, "\x00"); test != "" && !seen[test] {
			seen[test] = true
			names = append(names, regexp.QuoteMeta(test))
		}
	}
	if len(names) > 0 {
		sort.Strings(names)
		args = append(args, "-skip", "^("+strings.Join(names, "|")+")$")
	}
	args = append(args, s.pkgs...)
	cmd := exec.Command("go", args...)
	cmd.Dir = s.root
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	ownGroup(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	running.Store(cmd.Process.Pid, true)
	runErr := cmd.Wait()
	running.Delete(cmd.Process.Pid)
	out := Failures{}
	failedTests := map[string]bool{}
	var failedPkgs []string
	dec := json.NewDecoder(&stdout)
	for {
		var ev struct {
			Action  string
			Package string
			Test    string
		}
		if err := dec.Decode(&ev); err != nil {
			break
		}
		if ev.Action != "fail" {
			continue
		}
		if ev.Test == "" {
			failedPkgs = append(failedPkgs, ev.Package)
			continue
		}
		top, _, _ := strings.Cut(ev.Test, "/")
		out[ev.Package+"\x00"+top] = true
		failedTests[ev.Package] = true
	}
	for _, p := range failedPkgs {
		if !failedTests[p] {
			out[p+"\x00"] = true
		}
	}
	if runErr != nil && len(out) == 0 {
		// go test failed before running anything: a build error in a
		// package it could not name, or a bad flag.
		if line := firstLine(stderr.String()); line != "" {
			out["\x00"+line] = true
		} else {
			out["\x00"+runErr.Error()] = true
		}
	}
	return out, nil
}

// Since reports the first failure that is not in baseline.
func (f Failures) Since(baseline Failures) error {
	var fresh []string
	for k := range f {
		if !baseline[k] {
			fresh = append(fresh, k)
		}
	}
	if len(fresh) == 0 {
		return nil
	}
	sort.Strings(fresh)
	pkg, test, _ := strings.Cut(fresh[0], "\x00")
	switch {
	case pkg == "":
		return errors.New(test)
	case test == "":
		return fmt.Errorf("%s failed", pkg)
	}
	return fmt.Errorf("%s %s failed", pkg, test)
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
