package refactor

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"reflect"
	"sort"

	"github.com/dhilst/sx/internal/cost"
)

// Duplication is found by hashing every contiguous run of statements, with the
// identifiers and literals folded into the hash.
//
// Hashing structure alone would find far more - 66 repeats against 12 on this
// codebase - but structure alone is a guess about whether two pieces of code
// mean the same thing, and the graph holds nothing that could settle it. With
// identifiers included, a match is code that is literally the same.
//
// Runs rather than whole blocks, because duplication rarely lines up with a
// block boundary: the repeats in encoding/csv sit in the middle of three
// different functions.
const minDuplicateNodes = 12

// Occurrence is one appearance of a repeated run of statements.
type Occurrence struct {
	File      string `json:"file"`
	StartLine int    `json:"start_line"`
	StartCol  int    `json:"start_col"`
	EndLine   int    `json:"end_line"`
	EndCol    int    `json:"end_col"`
	Stmts     int    `json:"stmts"`

	// Byte offsets, because a statement run does not have to begin at the
	// start of a line or end at the end of one. Replacing whole lines
	// destroys whatever shares those lines - a `case` label, or a second
	// statement after a semicolon.
	StartOffset int `json:"start_offset"`
	EndOffset   int `json:"end_offset"`

	// The run itself, for the extraction model.
	stmts []ast.Stmt
	file  *ast.File
}

// DuplicateCandidates finds repeated code worth factoring out: runs the
// extraction model says the tree gets smaller without.
func DuplicateCandidates(dir string) ([]Candidate, error) {
	all, err := duplicates(dir)
	if err != nil {
		return nil, err
	}
	var out []Candidate
	for _, c := range all {
		if c.Predicted > 0 {
			out = append(out, c)
		}
	}
	// Sub-runs of a repeated run repeat too, so the same code appears at
	// several lengths. Keeping the largest at each site avoids counting one
	// finding three times.
	sort.SliceStable(out, func(i, j int) bool { return out[i].Predicted > out[j].Predicted })
	return maximal(out), nil
}

// duplicates is every repeated run with the model's price on it, including
// the ones it says would grow the tree.
func duplicates(dir string) ([]Candidate, error) { return NewCache().duplicates(dir) }

// Duplicates is DuplicateCandidates reusing what the cache already knows.
func (c *Cache) Duplicates(dir string) ([]Candidate, error) {
	all, err := c.duplicates(dir)
	if err != nil {
		return nil, err
	}
	var out []Candidate
	for _, cand := range all {
		if cand.Predicted > 0 {
			out = append(out, cand)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Predicted > out[j].Predicted })
	return maximal(out), nil
}

func (c *Cache) duplicates(dir string) ([]Candidate, error) {
	dirs, err := packageDirs(dir)
	if err != nil {
		return nil, err
	}
	var out []Candidate
	for _, d := range dirs {
		// Copies in different packages cannot share a function without
		// exporting it and importing it, which is a bigger change than this
		// makes and usually a worse one. So a candidate stays within one
		// package, and each package is worked out on its own and kept until
		// it changes.
		cs, err := remember(c, d, "dedup", nil, func() ([]Candidate, error) {
			return c.duplicatesIn(d)
		})
		if err != nil {
			continue
		}
		out = append(out, cs...)
	}
	return out, nil
}

// duplicatesIn finds the repeated runs in one package.
func (c *Cache) duplicatesIn(dir string) ([]Candidate, error) {
	tp, err := c.syntax(dir)
	if err != nil {
		return nil, err
	}
	var paths []string
	for path := range tp.files {
		if !generated(path) {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)

	type run struct {
		occ   []Occurrence
		nodes int
	}
	runs := map[[sha256.Size]byte]*run{}
	fset := tp.fset
	for _, path := range paths {
		f := tp.files[path]
		ast.Inspect(f, func(n ast.Node) bool {
			var list []ast.Stmt
			switch s := n.(type) {
			case *ast.BlockStmt:
				list = s.List
			case *ast.CaseClause:
				list = s.Body
			default:
				return true
			}
			if len(list) < 2 {
				return true
			}
			// Each statement is hashed and counted once; a run's hash and
			// size are made from its statements'. Hashing every run from
			// scratch walked each statement once per run containing it.
			digests := make([][sha256.Size]byte, len(list))
			sizes := make([]int, len(list)+1)
			for i, st := range list {
				digests[i] = stmtDigest(st)
				sizes[i+1] = sizes[i] + cost.Count(st)
			}
			for i := 0; i < len(list); i++ {
				for j := i + 2; j <= len(list); j++ {
					nodes := sizes[j] - sizes[i]
					if nodes < minDuplicateNodes {
						continue
					}
					h := runDigest(digests[i:j])
					if runs[h] == nil {
						runs[h] = &run{nodes: nodes}
					}
					seq := list[i:j]
					start := fset.Position(seq[0].Pos())
					end := fset.Position(seq[len(seq)-1].End())
					runs[h].occ = append(runs[h].occ, Occurrence{
						File: path, StartLine: start.Line, StartCol: start.Column,
						EndLine: end.Line, EndCol: end.Column, Stmts: len(seq),
						StartOffset: start.Offset, EndOffset: end.Offset,
						stmts: seq, file: f,
					})
				}
			}
			return true
		})
	}

	var out []Candidate
	var typed *typedPackage
	for h, r := range runs {
		d := len(r.occ)
		// The declaration costs at least five nodes and each call at least
		// three; below that nothing is worth type-checking for.
		if d < 2 || overlaps(r.occ) || 5+3*d-(d-1)*r.nodes >= 0 {
			continue
		}
		sort.SliceStable(r.occ, func(i, j int) bool {
			if r.occ[i].File != r.occ[j].File {
				return r.occ[i].File < r.occ[j].File
			}
			return r.occ[i].StartLine < r.occ[j].StartLine
		})
		if typed == nil {
			if typed, err = c.load(dir); err != nil {
				return nil, nil
			}
		}
		var copies []*ast.File
		for _, o := range r.occ {
			copies = append(copies, o.file)
		}
		// gopls extracts the first copy; the call it writes there is what
		// every other copy becomes.
		model, err := predictExtraction(typed, r.occ[0].file, r.occ[0].stmts, d, copies)
		if err != nil {
			continue
		}
		if copiesAgree(typed, r.occ) != nil {
			continue
		}
		hash := hex.EncodeToString(h[:])
		out = append(out, Candidate{
			Kind: KindDuplicate, File: r.occ[0].File, Line: r.occ[0].StartLine,
			Col: r.occ[0].StartCol, Target: "dup:" + hash[:8], Predicted: -model.Delta(),
			Occurrences: r.occ, Hash: hash, model: model,
			Detail: fmt.Sprintf("%d identical copies of %d nodes: %s", d, r.nodes, model),
		})
	}
	return out, nil
}

// maximal drops candidates whose sites are contained in a larger candidate's.
func maximal(in []Candidate) []Candidate {
	var out []Candidate
	claimed := map[string]bool{}
	for _, c := range in {
		taken := false
		for _, o := range c.Occurrences {
			for line := o.StartLine; line <= o.EndLine; line++ {
				if claimed[fmt.Sprintf("%s:%d", o.File, line)] {
					taken = true
				}
			}
		}
		if taken {
			continue
		}
		for _, o := range c.Occurrences {
			for line := o.StartLine; line <= o.EndLine; line++ {
				claimed[fmt.Sprintf("%s:%d", o.File, line)] = true
			}
		}
		out = append(out, c)
	}
	return out
}

// overlaps rejects a group whose occurrences are the same code counted twice.
func overlaps(occ []Occurrence) bool {
	for i := range occ {
		for j := i + 1; j < len(occ); j++ {
			if occ[i].File != occ[j].File {
				continue
			}
			if occ[i].StartLine <= occ[j].EndLine && occ[j].StartLine <= occ[i].EndLine {
				return true
			}
		}
	}
	return false
}

// FindRuns locates every statement run in a file whose hash matches, using the
// file as it is now.
//
// Positions recorded before a change are worthless afterwards: extracting the
// first occurrence removes lines and appends a function, so everything below it
// has moved. Re-finding by hash sidesteps the whole problem.
func FindRuns(path, hash string) ([]Occurrence, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, err
	}
	var out []Occurrence
	ast.Inspect(f, func(n ast.Node) bool {
		var list []ast.Stmt
		switch s := n.(type) {
		case *ast.BlockStmt:
			list = s.List
		case *ast.CaseClause:
			list = s.Body
		default:
			return true
		}
		for i := 0; i < len(list); i++ {
			for j := i + 2; j <= len(list); j++ {
				seq := list[i:j]
				if hashRun(seq) != hash {
					continue
				}
				start := fset.Position(seq[0].Pos())
				end := fset.Position(seq[len(seq)-1].End())
				out = append(out, Occurrence{
					File: path, StartLine: start.Line, StartCol: start.Column,
					EndLine: end.Line, EndCol: end.Column, Stmts: len(seq),
					StartOffset: start.Offset, EndOffset: end.Offset,
				})
			}
		}
		return true
	})
	return out, nil
}

func hashRun(seq []ast.Stmt) string {
	digests := make([][sha256.Size]byte, len(seq))
	for i, st := range seq {
		digests[i] = stmtDigest(st)
	}
	h := runDigest(digests)
	return hex.EncodeToString(h[:])
}

// stmtDigest identifies a statement by its structure, its identifiers and
// literals, and every token that is not a position: the operator in x += n,
// break or continue, the arrow of a channel type, the ... of a variadic call.
//
// It used to hash node types, identifiers and literals only. "count += n" and
// "count -= n" were then the same statement, the detector reported two runs
// differing in that line as copies, and the extraction replaced one with the
// other: it compiled, the tests had no case for it, and the tree got smaller.
func stmtDigest(s ast.Stmt) [sha256.Size]byte {
	h := sha256.New()
	ast.Inspect(s, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		// The node's type and its own non-node fields. Positions are left
		// out, except a variadic call's ..., which changes what it means.
		v := reflect.ValueOf(n).Elem()
		t := v.Type()
		io.WriteString(h, t.Name())
		for i := 0; i < t.NumField(); i++ {
			f, fv := t.Field(i), v.Field(i)
			switch {
			case f.Type == reflect.TypeOf(token.NoPos):
				if call, ok := n.(*ast.CallExpr); ok && f.Name == "Ellipsis" {
					fmt.Fprintf(h, "|...=%t", call.Ellipsis.IsValid())
				}
			case fv.Kind() == reflect.String, fv.Kind() == reflect.Bool, fv.Kind() == reflect.Int:
				fmt.Fprintf(h, "|%s=%v", f.Name, fv.Interface())
			}
		}
		io.WriteString(h, ";")
		return true
	})
	var d [sha256.Size]byte
	copy(d[:], h.Sum(nil))
	return d
}

// runDigest identifies a run of statements by theirs.
func runDigest(digests [][sha256.Size]byte) [sha256.Size]byte {
	h := sha256.New()
	for _, d := range digests {
		h.Write(d[:])
	}
	var out [sha256.Size]byte
	copy(out[:], h.Sum(nil))
	return out
}
