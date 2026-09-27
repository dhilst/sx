// Package edit rewrites Go source by byte spans taken from AST positions.
//
// Nothing here knows about any particular refactoring: it is the layer
// between "the AST says" and "the file now contains". Every change is an
// Edit of the original text, so spans computed from one parse stay valid
// however many edits are pending, and two edits that touch the same bytes are
// a conflict reported by Apply rather than a silently mangled file.
//
//	Span      a byte range of one buffer
//	Edit      replace a span with text (insert = empty span, delete = empty text)
//	Buffer    source + pending edits; edits must not overlap and are applied
//	          right to left, so every span refers to the original text
//	Sub       a buffer over a slice of another, with spans still given in the
//	          parent's offsets: used to edit a moved region before moving it
//
// Node-level helpers (ReplaceNode, DeleteNode, RenameIdents, WrapIdents)
// translate token.Pos to offsets through the FileSet.

package edit

import (
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"sort"
	"strings"
)

type Span struct{ Start, End int }

type Edit struct {
	Span
	Text string
	Why  string // for conflict reports
}

type Buffer struct {
	fset  *token.FileSet
	src   []byte
	base  int // offset of src[0] in the file (non-zero for a Sub buffer)
	edits []Edit
}

func NewBuffer(fset *token.FileSet, src []byte) *Buffer { return &Buffer{fset: fset, src: src} }

// Sub is a buffer over [span) of b's original text; its edits use file offsets.
func (b *Buffer) Sub(s Span) *Buffer {
	return &Buffer{fset: b.fset, src: b.src[s.Start-b.base : s.End-b.base], base: s.Start}
}

func (b *Buffer) Off(p token.Pos) int { return b.fset.Position(p).Offset }
func (b *Buffer) NodeSpan(n ast.Node) Span {
	return Span{b.Off(n.Pos()), b.Off(n.End())}
}
func (b *Buffer) Contains(s Span) bool {
	return s.Start >= b.base && s.End <= b.base+len(b.src)
}
func (b *Buffer) Text(s Span) string { return string(b.src[s.Start-b.base : s.End-b.base]) }

func (b *Buffer) Replace(s Span, text, why string) {
	b.edits = append(b.edits, Edit{s, text, why})
}
func (b *Buffer) Insert(at int, text, why string) { b.Replace(Span{at, at}, text, why) }
func (b *Buffer) Delete(s Span, why string)       { b.Replace(s, "", why) }

func (b *Buffer) ReplaceNode(n ast.Node, text, why string) { b.Replace(b.NodeSpan(n), text, why) }

// DeleteStmt removes a statement together with the rest of its line.
func (b *Buffer) DeleteStmt(n ast.Node, why string) {
	s := b.NodeSpan(n)
	for s.End-b.base < len(b.src) && b.src[s.End-b.base] != '\n' {
		s.End++
	}
	b.Delete(s, why)
}

// RenameIdents rewrites each identifier to name.
func (b *Buffer) RenameIdents(ids []*ast.Ident, name, why string) {
	for _, id := range ids {
		b.ReplaceNode(id, name, why)
	}
}

// WrapIdents rewrites each identifier with a template: "(*%s)" dereferences.
func (b *Buffer) WrapIdents(ids []*ast.Ident, tmpl, name, why string) {
	for _, id := range ids {
		b.ReplaceNode(id, fmt.Sprintf(tmpl, name), why)
	}
}

// Apply returns the edited text. Overlapping edits are an error: two
// rewrites of the same bytes cannot both be honoured. Two inserts at the
// same point are kept in the order they were made.
func (b *Buffer) Apply() (string, error) {
	edits := append([]Edit(nil), b.edits...)
	for k := range edits {
		if !b.Contains(edits[k].Span) {
			return "", fmt.Errorf("edit %q at %d-%d is outside the buffer", edits[k].Why, edits[k].Start, edits[k].End)
		}
	}
	idx := make([]int, len(edits))
	for k := range idx {
		idx[k] = k
	}
	// by start; at the same start, pure inserts go before the replacement
	sort.SliceStable(idx, func(i, j int) bool {
		p, q := edits[idx[i]], edits[idx[j]]
		if p.Start != q.Start {
			return p.Start < q.Start
		}
		return p.End == p.Start && q.End > q.Start
	})
	for k := 1; k < len(idx); k++ {
		p, q := edits[idx[k-1]], edits[idx[k]]
		if q.Start < p.End || (q.Start == p.Start && p.End > p.Start && q.End > q.Start) {
			return "", fmt.Errorf("overlapping edits: %q (%d-%d) and %q (%d-%d)", p.Why, p.Start, p.End, q.Why, q.Start, q.End)
		}
	}
	var out strings.Builder
	pos := b.base
	for _, k := range idx {
		e := edits[k]
		out.Write(b.src[pos-b.base : e.Start-b.base])
		out.WriteString(e.Text)
		pos = e.End
	}
	out.Write(b.src[pos-b.base:])
	return out.String(), nil
}

// Format applies the edits and gofmts the result (whole files only).
func (b *Buffer) Format() ([]byte, error) {
	text, err := b.Apply()
	if err != nil {
		return nil, err
	}
	out, err := format.Source([]byte(text))
	if err != nil {
		return []byte(text), fmt.Errorf("edited file does not parse: %w", err)
	}
	return out, nil
}

// EnsureImport adds `import "path"` to file unless it is already imported.
// It goes into the first parenthesised import block, or after the package
// clause; gofmt sorts it afterwards.
func (b *Buffer) EnsureImport(file *ast.File, path string) bool {
	for _, imp := range file.Imports {
		if strings.Trim(imp.Path.Value, `"`) == path {
			return false
		}
	}
	for _, d := range file.Decls {
		if g, ok := d.(*ast.GenDecl); ok && g.Tok == token.IMPORT && g.Lparen.IsValid() {
			b.Insert(b.Off(g.Rparen), fmt.Sprintf("\t%q\n", path), "import "+path)
			return true
		}
	}
	b.Insert(b.Off(file.Name.End()), fmt.Sprintf("\n\nimport %q", path), "import "+path)
	return true
}

// Render returns the text of span with the pending edits inside it applied.
// Used to rewrite a statement whose parts were already edited (renamed,
// dereferenced) without the rewrite and those edits overlapping.
func (b *Buffer) Render(s Span) (string, error) {
	sub := &Buffer{fset: b.fset, src: b.src[s.Start-b.base : s.End-b.base], base: s.Start}
	for _, e := range b.edits {
		if e.Start >= s.Start && e.End <= s.End && !(e.Start == e.End && (e.Start == s.Start || e.Start == s.End)) {
			sub.edits = append(sub.edits, e)
		}
	}
	return sub.Apply()
}

// ReplaceRendered replaces span with text, dropping the edits inside it:
// text is expected to have been built from Render of (parts of) span.
func (b *Buffer) ReplaceRendered(s Span, text, why string) {
	kept := b.edits[:0]
	for _, e := range b.edits {
		inside := e.Start >= s.Start && e.End <= s.End && !(e.Start == e.End && (e.Start == s.Start || e.Start == s.End))
		if !inside {
			kept = append(kept, e)
		}
	}
	b.edits = append(kept, Edit{s, text, why})
}
