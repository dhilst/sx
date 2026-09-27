package edit

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestBufferEdits(t *testing.T) {
	fset := token.NewFileSet()
	src := []byte("0123456789")
	b := NewBuffer(fset, src)
	b.Replace(Span{2, 4}, "ab", "r1")
	b.Insert(6, "X", "i1")
	b.Insert(6, "Y", "i2") // same point: kept in order
	b.Delete(Span{8, 9}, "d1")
	got, err := b.Apply()
	if err != nil || got != "01ab45XY679" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestBufferOverlap(t *testing.T) {
	b := NewBuffer(token.NewFileSet(), []byte("0123456789"))
	b.Replace(Span{2, 5}, "a", "first")
	b.Replace(Span{4, 6}, "b", "second")
	if _, err := b.Apply(); err == nil || !strings.Contains(err.Error(), "overlapping") {
		t.Fatalf("want overlap error, got %v", err)
	}
}

func TestSubBufferUsesFileOffsets(t *testing.T) {
	b := NewBuffer(token.NewFileSet(), []byte("func f() { x := 1; y := x }"))
	sub := b.Sub(Span{11, 25}) // "x := 1; y := x"
	sub.Replace(Span{11, 12}, "z", "rename")
	sub.Replace(Span{24, 25}, "z", "rename")
	got, err := sub.Apply()
	if err != nil || got != "z := 1; y := z" {
		t.Fatalf("got %q, %v", got, err)
	}
	sub.Replace(Span{0, 3}, "no", "outside")
	if _, err := sub.Apply(); err == nil {
		t.Fatal("edit outside a sub buffer must fail")
	}
}

func TestEnsureImport(t *testing.T) {
	for _, src := range []string{
		"package p\n\nimport (\n\t\"fmt\"\n)\n\nvar _ = fmt.Sprint\n",
		"package p\n\nvar x = 1\n",
	} {
		fset := token.NewFileSet()
		f, _ := parser.ParseFile(fset, "p.go", src, 0)
		b := NewBuffer(fset, []byte(src))
		if !b.EnsureImport(f, "hash") || b.EnsureImport(f, "fmt") && strings.Contains(src, "fmt") {
			t.Fatal("wrong EnsureImport result")
		}
		out, err := b.Format()
		if err != nil || !strings.Contains(string(out), `"hash"`) {
			t.Fatalf("%s %v", out, err)
		}
	}
}

func TestInsertBeforeReplaceAtSameStart(t *testing.T) {
	b := NewBuffer(token.NewFileSet(), []byte("return x"))
	b.Replace(Span{7, 8}, "(*x)", "deref") // made first
	b.Insert(7, "a, ", "prefix")           // same start: must still come first
	b.Insert(8, ", 1", "suffix")
	got, err := b.Apply()
	if err != nil || got != "return a, (*x), 1" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestRenderAndReplaceRendered(t *testing.T) {
	src := "defer f(x, y)"
	b := NewBuffer(token.NewFileSet(), []byte(src))
	b.Replace(Span{8, 9}, "(*x)", "deref")
	b.Replace(Span{11, 12}, "y_", "rename")
	call := Span{6, 13}
	text, err := b.Render(call)
	if err != nil || text != "f((*x), y_)" {
		t.Fatalf("render %q %v", text, err)
	}
	b.ReplaceRendered(Span{0, 13}, "*d = append(*d, func() { "+text+" })", "defer adapter")
	got, err := b.Apply()
	if err != nil || got != "*d = append(*d, func() { f((*x), y_) })" {
		t.Fatalf("got %q %v", got, err)
	}
}
