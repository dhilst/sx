package refactor

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"

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
const (
	minDuplicateNodes = 12
	funcOverhead      = 12 // the declaration, its name, signature and body
	callCost          = 3  // the call, the name it calls, an argument
)

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
}

// DuplicateCandidates finds repeated code worth factoring out.
func DuplicateCandidates(dir string) ([]Candidate, error) {
	files, err := editableFilesIn(dir)
	if err != nil {
		return nil, err
	}
	type run struct {
		occ   []Occurrence
		nodes int
	}
	runs := map[string]*run{}

	for _, path := range files {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			continue
		}
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
					nodes := 0
					for _, s := range seq {
						nodes += cost.Count(s)
					}
					if nodes < minDuplicateNodes {
						continue
					}
					start := fset.Position(seq[0].Pos())
					end := fset.Position(seq[len(seq)-1].End())
					// Copies in different packages cannot share a function
					// without exporting it and importing it, which is a
					// bigger change than this makes and usually a worse one.
					// Grouping per directory keeps a candidate to one package.
					h := filepath.Dir(path) + "\x00" + hashRun(seq)
					if runs[h] == nil {
						runs[h] = &run{nodes: nodes}
					}
					runs[h].occ = append(runs[h].occ, Occurrence{
						File: path, StartLine: start.Line, StartCol: start.Column,
						EndLine: end.Line, EndCol: end.Column, Stmts: len(seq),
						StartOffset: start.Offset, EndOffset: end.Offset,
					})
				}
			}
			return true
		})
	}

	var out []Candidate
	for h, r := range runs {
		if len(r.occ) < 2 || overlaps(r.occ) {
			continue
		}
		saving := (len(r.occ)-1)*r.nodes - funcOverhead - len(r.occ)*callCost
		if saving <= 0 {
			continue
		}
		sort.SliceStable(r.occ, func(i, j int) bool {
			if r.occ[i].File != r.occ[j].File {
				return r.occ[i].File < r.occ[j].File
			}
			return r.occ[i].StartLine < r.occ[j].StartLine
		})
		hash := h[strings.IndexByte(h, 0)+1:]
		out = append(out, Candidate{
			Kind: KindDuplicate, File: r.occ[0].File, Line: r.occ[0].StartLine,
			Col: r.occ[0].StartCol, Target: "dup:" + hash[:8], Predicted: saving,
			Occurrences: r.occ, Hash: hash,
			Detail: fmt.Sprintf("%d identical copies of %d nodes across %d sites",
				len(r.occ), r.nodes, len(r.occ)),
		})
	}
	// Sub-runs of a repeated run repeat too, so the same code appears at
	// several lengths. Keeping the largest at each site avoids counting one
	// finding three times.
	sort.SliceStable(out, func(i, j int) bool { return out[i].Predicted > out[j].Predicted })
	return maximal(out), nil
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
	h := sha256.New()
	for _, s := range seq {
		ast.Inspect(s, func(n ast.Node) bool {
			if n == nil {
				return false
			}
			fmt.Fprintf(h, "%T;", n)
			switch x := n.(type) {
			case *ast.Ident:
				fmt.Fprintf(h, "id=%s;", x.Name)
			case *ast.BasicLit:
				fmt.Fprintf(h, "lit=%s;", x.Value)
			}
			return true
		})
	}
	return hex.EncodeToString(h.Sum(nil))
}
