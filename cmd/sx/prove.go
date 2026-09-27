package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dhilst/sx/internal/refactor"
	"github.com/dhilst/sx/internal/semhash"
)

// proveChanges is -prove: record, for every change, whether it is proved to
// keep the package's behaviour (see internal/semhash). The record never
// decides anything: the build and the tests still do.
var proveChanges = true

// proof holds the package a change is about to touch, as it was before.
type proof struct {
	dir    string
	before *semhash.Snapshot
	err    error
}

// snapshotBefore loads the candidate's package before the change is applied.
func snapshotBefore(root string, c refactor.Candidate) *proof {
	if !proveChanges || c.File == "" {
		return nil
	}
	file := c.File
	if !filepath.IsAbs(file) {
		file = filepath.Join(root, file)
	}
	p := &proof{dir: filepath.Dir(file)}
	p.before, p.err = semhash.Load(p.dir)
	return p
}

// verdict compares the package now with the snapshot: "proved ..." or
// "unproved: why". Empty when -prove is off.
func (p *proof) verdict() (v string, proved bool) {
	if p == nil {
		return "", false
	}
	if p.err != nil {
		return "unproved: loading the package before the change: " + firstLine(p.err.Error()), false
	}
	after, err := semhash.Load(p.dir)
	if err != nil {
		return "unproved: loading the package after the change: " + firstLine(err.Error()), false
	}
	defer func() {
		if r := recover(); r != nil { // a recorder must never end the run
			v, proved = fmt.Sprintf("unproved: internal error: %v", r), false
		}
	}()
	res := semhash.Prove(p.before, after, semhash.Options{})
	return res.String(), res.Proved
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return s
}

// shortVerdict fits a verdict on a change line.
func shortVerdict(v string) string {
	if len(v) > 100 {
		return v[:97] + "..."
	}
	return v
}
