package refactor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, name, src string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Repeated code is found by hashing runs of statements, not whole blocks:
// duplication rarely lines up with a brace.
func TestDuplicateRunsAreFoundInsideBlocks(t *testing.T) {
	dir := write(t, "p.go", `package p

import "fmt"

func A(xs []int) {
	fmt.Println("A")
	for _, x := range xs {
		scaled := x * 2
		adjusted := scaled + 1
		fmt.Println(scaled, adjusted)
		fmt.Println(adjusted * scaled)
	}
}

func B(xs []int) {
	fmt.Println("B")
	for _, x := range xs {
		scaled := x * 2
		adjusted := scaled + 1
		fmt.Println(scaled, adjusted)
		fmt.Println(adjusted * scaled)
	}
}
`)
	got, err := DuplicateCandidates(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("the repeated loop body was not found")
	}
	c := got[0]
	if len(c.Occurrences) != 2 {
		t.Errorf("found %d occurrences, want 2", len(c.Occurrences))
	}
	if c.Predicted <= 0 {
		t.Errorf("a repeat worth factoring should predict a saving, got %d", c.Predicted)
	}
	if c.Hash == "" {
		t.Error("a duplicate must carry its hash, or the copies cannot be found again after the first is extracted")
	}
}

// Code that merely looks alike is not a duplicate. The hash includes
// identifiers and literals, so a difference in either is a difference.
func TestDifferentIdentifiersAreNotDuplicates(t *testing.T) {
	dir := write(t, "p.go", `package p

func A(xs []int) int {
	total := 0
	for _, x := range xs {
		scaled := x * 2
		adjusted := scaled + 1
		total += adjusted
		total += scaled
	}
	return total
}

func B(ys []int) int {
	sum := 0
	for _, y := range ys {
		doubled := y * 3
		bumped := doubled + 9
		sum += bumped
		sum += doubled
	}
	return sum
}
`)
	got, err := DuplicateCandidates(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("same shape with different names was reported as duplication: %+v", got[0].Detail)
	}
}

// Two copies in different packages cannot share a function without exporting
// and importing it, so they are not offered as one candidate.
func TestDuplicatesAreNotGroupedAcrossPackages(t *testing.T) {
	dir := t.TempDir()
	body := `	total := 0
	for _, x := range xs {
		scaled := x * 2
		adjusted := scaled + 1
		total += adjusted
		total += scaled
	}
	return total
`
	for _, pkg := range []string{"one", "two"} {
		sub := filepath.Join(dir, pkg)
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		src := "package " + pkg + "\n\nfunc F(xs []int) int {\n" + body + "}\n"
		if err := os.WriteFile(filepath.Join(sub, "p.go"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := DuplicateCandidates(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range got {
		dirs := map[string]bool{}
		for _, o := range c.Occurrences {
			dirs[filepath.Dir(o.File)] = true
		}
		if len(dirs) > 1 {
			t.Errorf("a candidate spans %d packages", len(dirs))
		}
	}
}

// A duplicate is identified by what it is, not where: every applied change
// moves the lines below it, and a position-based key let one rejected
// candidate come back seven times in a single run.
func TestDuplicateKeyIsStableUnderMovement(t *testing.T) {
	a := Candidate{Kind: KindDuplicate, Hash: "abc123", File: "x.go", Line: 10}
	b := Candidate{Kind: KindDuplicate, Hash: "abc123", File: "x.go", Line: 40}
	if a.Key() != b.Key() {
		t.Errorf("the same duplicate got two keys: %q and %q", a.Key(), b.Key())
	}
	c := Candidate{Kind: KindInline, Target: "f", File: "x.go", Line: 10}
	if c.Key() == a.Key() {
		t.Error("different kinds must not share a key")
	}
	egA := Candidate{Kind: KindEg, Template: "examples/eg/time-since.go", File: "x.go", Line: 10}
	egB := Candidate{Kind: KindEg, Template: "examples/eg/time-since.go", File: "x.go", Line: 40}
	if egA.Key() != egB.Key() {
		t.Errorf("the same eg template got two keys: %q and %q", egA.Key(), egB.Key())
	}
}

func TestParseEgMatches(t *testing.T) {
	got, first, err := parseEgMatches("/repo", `=== /repo/a.go (2 matches)
=== b.go (1 matches)
`)
	if err != nil {
		t.Fatal(err)
	}
	if got != 3 {
		t.Fatalf("matches = %d, want 3", got)
	}
	if first != "/repo/a.go" {
		t.Fatalf("first file = %q, want /repo/a.go", first)
	}
}

// A wildcard the template drops saves the expression it bound, not one
// node: s[:len(s)] -> s on x.s removes the pattern's four nodes and the
// second copy of x.s.
func TestEgModelCountsDroppedWildcards(t *testing.T) {
	dir := write(t, "p.go", `package p

type T struct{ s string }

func A(x T) string { return x.s[:len(x.s)] }
`)
	tmpl := filepath.Join(t.TempDir(), "full-slice.go")
	if err := os.WriteFile(tmpl, []byte(`//go:build ignore

package template

func before(s string) string { return s[:len(s)] }
func after(s string) string  { return s }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := predictEg(packages{}, dir, tmpl)
	if err != nil {
		t.Fatal(err)
	}
	if m.Matches != 1 || m.P != -4 || m.W != -2 || m.Delta() != -6 {
		t.Fatalf("model = %s, want 1 match, P=-4, W=-2, ΔN=-6", m)
	}
}

func TestEgTemplatesExpandsMultiplePaths(t *testing.T) {
	root := t.TempDir()
	one := filepath.Join(root, "one.go")
	twoDir := filepath.Join(root, "rules")
	two := filepath.Join(twoDir, "two.go")
	if err := os.WriteFile(one, []byte("package template\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(twoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(two, []byte("package template\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := EgTemplates([]string{one, twoDir, filepath.Join(root, "missing")})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("templates = %v, want two files", got)
	}
	if got[0] != one || got[1] != two {
		t.Fatalf("templates = %v, want sorted [%s %s]", got, one, two)
	}
}

// An unused import is the one build failure worth repairing: nothing can
// depend on it. Anything else is the transformation being wrong, not unfinished.
func TestOnlyUnusedImportsAreConsideredRepairable(t *testing.T) {
	if !(BuildProblem{Message: `"fmt" imported and not used`}).Repairable() {
		t.Error("an unused import should be repairable")
	}
	for _, msg := range []string{"undefined: helper", "declared and not used: x", "too many return values"} {
		if (BuildProblem{Message: msg}).Repairable() {
			t.Errorf("%q should not be treated as repairable", msg)
		}
	}
}

// The function gopls extracts is built out of the run, so it contains a copy of
// it and matches the hash like any other occurrence. Replacing that copy turns
// the function into a call to itself. It compiles, it measures as smaller, the
// tests pass because nothing covers it, and it never returns - which is exactly
// what shipped before this was excluded.
func TestTheExtractedFunctionIsNotItselfReplaced(t *testing.T) {
	dir := write(t, "p.go", `package p

func A(xs []int) int {
	total := 0
	count := 0
	for _, x := range xs {
		total += x * 2
		count++
	}
	return total + count
}

func helper(xs []int) int {
	total := 0
	count := 0
	for _, x := range xs {
		total += x * 2
		count++
	}
	return total + count
}
`)
	path := filepath.Join(dir, "p.go")
	runs, err := FindRuns(path, hashOf(t, path))
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) < 2 {
		t.Fatalf("expected the run to be found in both functions, got %d", len(runs))
	}
	start, end, err := declSpan(path, "helper")
	if err != nil {
		t.Fatal(err)
	}
	kept := outside(runs, start, end)
	if len(kept) != len(runs)-1 {
		t.Fatalf("expected exactly the copy inside helper to be dropped: %d of %d kept",
			len(kept), len(runs))
	}
	for _, o := range kept {
		if o.StartOffset >= start && o.EndOffset <= end {
			t.Fatalf("a run inside helper survived at offset %d", o.StartOffset)
		}
	}
}

// hashOf returns the hash of the first run the detector reports in a file.
func hashOf(t *testing.T, path string) string {
	t.Helper()
	cs, err := DuplicateCandidates(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) == 0 {
		t.Fatal("no duplicate found in the fixture")
	}
	return cs[0].Hash
}

// Formatting is outside the measure, so nothing else in the pipeline objects to
// a change that leaves the tree unreadable.
func TestFormatRepairsWhatTheMeasureCannotSee(t *testing.T) {
	dir := write(t, "p.go", "package p\n\nfunc A() int {\nreturn 1\n}\n")
	if err := Format(dir, nil); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "p.go"))
	if err != nil {
		t.Fatal(err)
	}
	want := "package p\n\nfunc A() int {\n\treturn 1\n}\n"
	if string(got) != want {
		t.Fatalf("Format left the file unformatted:\n%q", got)
	}
}

// A file that does not parse is a build problem, not a formatting one; Format
// must leave it alone rather than destroy it.
func TestFormatLeavesUnparseableFilesAlone(t *testing.T) {
	dir := write(t, "p.go", "package p\n\nfunc A( {\n")
	if err := Format(dir, nil); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "p.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "package p\n\nfunc A( {\n" {
		t.Fatalf("Format touched a file it could not parse:\n%q", got)
	}
}

// A value returned by copy after its address was taken detaches whatever
// holds the address. gopls extracted `var buf bytes.Buffer; cmd.Stderr = &buf`
// into a function returning the buffer by value - every caller got a detached
// copy. It built, the tests passed, and the measure fell, so the model has to
// refuse it.
func TestAddressTakenResultsAreNotExtracted(t *testing.T) {
	dir := write(t, "p.go", `package p

import (
	"bytes"
	"os/exec"
)

func A(cmd *exec.Cmd) string {
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &stderr
	cmd.Run()
	return stderr.String()
}

func B(cmd *exec.Cmd) string {
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &stderr
	cmd.Run()
	return stderr.String() + "!"
}
`)
	all, err := duplicates(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range all {
		for _, name := range c.model.(Extraction).Results {
			if name == "stderr" {
				t.Fatalf("stderr is returned by value after &stderr: %s", c.Detail)
			}
		}
	}
}

// The text that replaces each copy is everything gopls put in place of the
// first one, not just the statement holding the call.
func TestCallSiteIncludesTheReturnCheck(t *testing.T) {
	before := `package p

import "strconv"

func A(s string) (int, error) {
	x, err := strconv.Atoi(s)
	if err != nil {
		return 0, err
	}
	return x + 1, nil
}
`
	dir := write(t, "p.go", before)
	path := filepath.Join(dir, "p.go")
	start := strings.Index(before, "x, err")
	end := strings.Index(before, "\treturn x + 1") - 1
	after := `package p

import "strconv"

func A(s string) (int, error) {
	x, i, err := newFunction(s)
	if err != nil {
		return i, err
	}
	return x + 1, nil
}

func newFunction(s string) (int, int, error) {
	x, err := strconv.Atoi(s)
	if err != nil {
		return 0, 0, err
	}
	return x, 0, nil
}
`
	if err := os.WriteFile(path, []byte(after), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := callSite([]byte(before), Occurrence{StartOffset: start, EndOffset: end}, path, "newFunction")
	if err != nil {
		t.Fatal(err)
	}
	want := "x, i, err := newFunction(s)\nif err != nil {\n\treturn i, err\n}"
	if got != want {
		t.Fatalf("call site = %q, want %q", got, want)
	}
}

// A helper the tests call is not called once. Counting only the files that
// ship made three of them look inlinable every pass; each deleted a
// declaration the tests still named, failed, and came back next time.
func TestFunctionsTheTestsCallAreNotInlineCandidates(t *testing.T) {
	dir := write(t, "p.go", `package p

func A() int { return helper(1) }

func helper(n int) int { return n + 1 }
`)
	if err := os.WriteFile(filepath.Join(dir, "p_test.go"), []byte(`package p

import "testing"

func TestHelper(t *testing.T) {
	if helper(1) != 2 {
		t.Fatal("no")
	}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	cs, err := InlineCandidates(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cs {
		if c.Target == "helper" {
			t.Fatalf("helper has two callers, one of them a test, but was offered for inlining")
		}
	}
}

// An inline candidate is the function, not the place it is called from. Keying
// on the position meant an accepted change below a rejected candidate renamed
// its key and offered it again.
func TestInlineKeySurvivesTheCallSiteMoving(t *testing.T) {
	a := Candidate{Kind: KindInline, File: "/x/p.go", Line: 10, Col: 2, Target: "helper"}
	b := Candidate{Kind: KindInline, File: "/x/p.go", Line: 40, Col: 6, Target: "helper"}
	if a.Key() != b.Key() {
		t.Fatalf("the same function keyed two ways: %q and %q", a.Key(), b.Key())
	}
	other := Candidate{Kind: KindInline, File: "/y/p.go", Line: 10, Col: 2, Target: "helper"}
	if a.Key() == other.Key() {
		t.Fatalf("two packages' helpers share a key: %q", a.Key())
	}
}

func TestMultiStatementFunctionsCalledInExpressionContextAreNotInlineCandidates(t *testing.T) {
	dir := write(t, "p.go", `package p

func A(xs []int) int {
	return helper(xs)
}

func helper(xs []int) int {
	total := 0
	for _, x := range xs {
		total += x
	}
	return total
}
`)
	cs, err := InlineCandidates(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cs {
		if c.Target == "helper" {
			t.Fatal("a multi-statement helper called from expression context was offered for inlining")
		}
	}
}

func TestSingleReturnFunctionsCalledInExpressionContextRemainInlineCandidates(t *testing.T) {
	dir := write(t, "p.go", `package p

func A(n int) int {
	return helper(n)
}

func helper(n int) int {
	return n + 1
}
`)
	cs, err := InlineCandidates(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cs {
		if c.Target == "helper" {
			return
		}
	}
	t.Fatal("a single-return helper called from expression context should still be offered")
}

// A generated file says not to edit it, and it means it: the change is erased
// the next time the generator runs. The loop rewrote index/suffixarray/sais2.go
// before this, stripping 512 lines of comments from a generated algorithm.
func TestGeneratedFilesAreNotEdited(t *testing.T) {
	const src = `// Copyright 2019 The Go Authors.

// Code generated by go generate; DO NOT EDIT.

package p

func A() int { return helper(1) }

func helper(n int) int { return n + 1 }
`
	dir := write(t, "gen.go", src)
	path := filepath.Join(dir, "gen.go")
	if !generated(path) {
		t.Fatal("the DO NOT EDIT marker was not recognised")
	}
	cs, err := InlineCandidates(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 0 {
		t.Fatalf("offered %d candidates in a generated file", len(cs))
	}
	if _, err := removeDecl(path, "helper"); err == nil {
		t.Fatal("removeDecl wrote to a generated file")
	}
}

// The marker only counts before the package clause. A file that merely mentions
// the phrase further down is ordinary source.
func TestTheGeneratedMarkerOnlyCountsInTheHeader(t *testing.T) {
	dir := write(t, "p.go", `package p

// Code generated by go generate; DO NOT EDIT.
func A() int { return 1 }
`)
	if generated(filepath.Join(dir, "p.go")) {
		t.Fatal("a marker after the package clause was treated as a header")
	}
}

// An unexported function belongs to its package. Keying by bare name across a
// tree conflated same-named helpers in different packages - and the platform
// variants of one name in a single package, where syscall declares fcntl six
// times - and then removed whichever declaration was parsed last.
func TestSameNameInTwoPackagesIsNotOneFunction(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, pkg := range []string{"a", "b"} {
		d := filepath.Join(root, pkg)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		src := "package " + pkg + `

func Use() int { return helper(1) }

func helper(n int) int { return n + 1 }
`
		if err := os.WriteFile(filepath.Join(d, "p.go"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cs, err := InlineCandidates(root)
	if err != nil {
		t.Fatal(err)
	}
	// One candidate per package: each helper has exactly one caller, its own.
	seen := map[string]bool{}
	for _, c := range cs {
		if c.Target == "helper" {
			seen[filepath.Dir(c.File)] = true
		}
	}
	if len(seen) != 2 {
		t.Fatalf("expected helper to be a candidate in both packages, got %d: %v", len(seen), seen)
	}
}

// Two files in one package declaring the same name are platform variants.
// Which one a call resolves to is a build-tag question, not a syntactic one.
func TestPlatformVariantsAreNotInlined(t *testing.T) {
	dir := write(t, "p_linux.go", `//go:build linux

package p

func Use() int { return helper(1) }

func helper(n int) int { return n + 1 }
`)
	if err := os.WriteFile(filepath.Join(dir, "p_windows.go"), []byte(`//go:build windows

package p

func helper(n int) int { return n + 2 }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	cs, err := InlineCandidates(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cs {
		if c.Target == "helper" {
			t.Fatal("a name declared once per platform was offered for inlining")
		}
	}
}

// A package directory holds more than one build. reflect has map_noswiss.go,
// crypto/ecdsa has boring.go, and gob has a debug.go that says in a comment it
// is not part of the package. `go build` and `go test` never compile them, so
// they cannot say whether an edit to one is safe - and dedup writes to files
// directly, with no gopls to refuse on its behalf.
func TestFilesThisBuildDoesNotCompileAreNotEdited(t *testing.T) {
	dir := write(t, "debug.go", `// Delete the next line to include in the package.
//
//go:build ignore

package p

func A() int { return helper(1) }

func helper(n int) int { return n + 1 }
`)
	if inCurrentBuild(filepath.Join(dir, "debug.go")) {
		t.Fatal("a //go:build ignore file was treated as part of this build")
	}
	cs, err := InlineCandidates(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 0 {
		t.Fatalf("offered %d candidates in a file no build compiles", len(cs))
	}
	files, err := editableFilesIn(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("offered %v as editable", files)
	}
}

// A constraint that excludes this host is the same problem: the file is real
// source somewhere, and nothing here can compile it to find out.
func TestFilesForAnotherPlatformAreNotEdited(t *testing.T) {
	dir := write(t, "p_plan9.go", `//go:build plan9

package p

func A() int { return 1 }
`)
	if inCurrentBuild(filepath.Join(dir, "p_plan9.go")) {
		t.Skip("this host builds plan9 files")
	}
	files, err := editableFilesIn(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "p_plan9.go") {
			t.Fatal("a file for another platform was offered as editable")
		}
	}
}

// A function can be named without being called. The standard library reaches
// its own unexported code with `var Exported = unexported` in an export_test.go,
// and that reference keeps the function alive however few times it is called.
func TestAFunctionHeldAsAValueIsNotInlinable(t *testing.T) {
	dir := write(t, "p.go", `package p

func A() int { return helper(1) }

func helper(n int) int { return n + 1 }
`)
	if err := os.WriteFile(filepath.Join(dir, "export_test.go"), []byte(`package p

var Helper = helper
`), 0o644); err != nil {
		t.Fatal(err)
	}
	cs, err := InlineCandidates(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cs {
		if c.Target == "helper" {
			t.Fatal("a function held as a value was offered for inlining")
		}
	}
}

// The plain case still works: one declaration, one call, nothing else.
func TestASinglyCalledFunctionIsStillInlinable(t *testing.T) {
	dir := write(t, "p.go", `package p

func A() int { return helper(1) }

func helper(n int) int { return n + 1 }
`)
	cs, err := InlineCandidates(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cs {
		if c.Target == "helper" {
			return
		}
	}
	t.Fatal("a function called exactly once was not offered for inlining")
}

// A //go:nosplit function has a fixed stack budget and no split check. Moving
// a body into it is the one thing the directive forbids, and nothing in the
// build or the tests reports it. internal/runtime/atomic has exactly this
// shape: panicUnaligned, called once, from a //go:nosplit lockAndCheck.
func TestNothingIsInlinedIntoADirectedFunction(t *testing.T) {
	dir := write(t, "p.go", `package p

//go:nosplit
func lockAndCheck(n int) int {
	return panicUnaligned(n)
}

func panicUnaligned(n int) int { return n + 1 }
`)
	cs, err := InlineCandidates(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cs {
		if c.Target == "panicUnaligned" {
			t.Fatal("a body was offered for inlining into a //go:nosplit function")
		}
	}
}

// The directive is the point of the declaration: inlining discards it, and
// removing the function takes it away with the doc comment.
func TestADirectedFunctionIsNotItselfInlined(t *testing.T) {
	dir := write(t, "p.go", `package p

func A(n int) int { return helper(n) }

//go:noinline
func helper(n int) int { return n + 1 }
`)
	cs, err := InlineCandidates(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cs {
		if c.Target == "helper" {
			t.Fatal("a //go:noinline function was offered for inlining")
		}
	}
}

// //go:generate instructs a tool, not the compiler, and constrains nothing.
func TestGoGenerateIsNotACompilerDirective(t *testing.T) {
	dir := write(t, "p.go", `package p

func A(n int) int { return helper(n) }

//go:generate stringer -type=T
func helper(n int) int { return n + 1 }
`)
	cs, err := InlineCandidates(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cs {
		if c.Target == "helper" {
			return
		}
	}
	t.Fatal("a //go:generate comment blocked an inline it does not constrain")
}

// Go allows a method to share a name with a plain function. Matching on the
// name alone deleted whichever came first in the file, so removing the function
// took the method instead.
func TestRemovingAFunctionDoesNotTakeAMethodOfTheSameName(t *testing.T) {
	dir := write(t, "p.go", `package p

type T struct{}

func (t *T) helper() int { return 1 }

func helper() int { return 2 }
`)
	path := filepath.Join(dir, "p.go")
	if _, err := removeDecl(path, "helper"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "func (t *T) helper() int") {
		t.Fatalf("the method was removed instead of the function:\n%s", got)
	}
	if strings.Contains(string(got), "func helper() int") {
		t.Fatalf("the function was not removed:\n%s", got)
	}
}

// A method cannot be removed by name, so it should never be proposed.
func TestDeadMethodsAreNotProposedForRemoval(t *testing.T) {
	dir := write(t, "p.go", `package p

type T struct{}

func (t *T) orphan() int { return 1 }
`)
	if _, err := removeDecl(filepath.Join(dir, "p.go"), "orphan"); err == nil {
		t.Fatal("removeDecl claimed to remove a method")
	}
}

// The measure does not count tests, so inlining into one and removing the
// declaration moves the body out of what is measured rather than removing it -
// and the measure records the move as a saving. On a two-function package that
// reads as 29 nodes down to 12, with nothing deleted.
func TestAHelperUsedOnlyByTestsIsNotInlined(t *testing.T) {
	dir := write(t, "p.go", `package p

func Exported() int { return 1 }

func onlyTestsUseThis(n int) int { return n*2 + 1 }
`)
	if err := os.WriteFile(filepath.Join(dir, "p_test.go"), []byte(`package p

import "testing"

func TestIt(t *testing.T) {
	if onlyTestsUseThis(2) != 5 {
		t.Fatal("no")
	}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	cs, err := InlineCandidates(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cs {
		if c.Target == "onlyTestsUseThis" {
			t.Fatal("a change was offered that moves code into a file the measure ignores")
		}
	}
}

// Format touches only what the change touched. Formatting the whole tree
// rewrites files the change never went near, and the revert restores only the
// files it wrote - so a rejected change would leave those reformatted for good.
func TestFormatLeavesUntouchedFilesAlone(t *testing.T) {
	dir := write(t, "stale.go", "package p\n\nfunc A() int {\nreturn 1\n}\n")
	if err := os.WriteFile(filepath.Join(dir, "fresh.go"), []byte("package p\n\nfunc B() int {\nreturn 2\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Everything as it stands now is not this change's business.
	snapshot, err := Stamp(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fresh.go"), []byte("package p\n\nfunc B() int {\nreturn 3\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Format(dir, snapshot); err != nil {
		t.Fatal(err)
	}
	stale, err := os.ReadFile(filepath.Join(dir, "stale.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(stale) != "package p\n\nfunc A() int {\nreturn 1\n}\n" {
		t.Fatalf("a file the change never touched was reformatted:\n%s", stale)
	}
	fresh, err := os.ReadFile(filepath.Join(dir, "fresh.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(fresh) != "package p\n\nfunc B() int {\n\treturn 3\n}\n" {
		t.Fatalf("the file the change touched was not formatted:\n%s", fresh)
	}
}

// What unsafe.Pointer guarantees depends on where the thing it points at lives
// and how long it stays alive, so moving such a body into another frame is a
// change neither the build nor the tests can see.
// internal/reflectlite.packEface is the case this came from: built around
// unsafe.Pointer(&i), carrying a comment that its correctness depends on no
// operation coming between two assignments, because the collector must not
// observe the half-built interface. The loop deleted it and pasted the body
// into its caller, and everything stayed green.
func TestBodiesUsingUnsafeAreNotMoved(t *testing.T) {
	dir := write(t, "p.go", `package p

import "unsafe"

func A(p *int) uintptr { return packIt(p) }

func packIt(p *int) uintptr {
	var i any = p
	return uintptr(unsafe.Pointer(&i))
}
`)
	cs, err := InlineCandidates(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cs {
		if c.Target == "packIt" {
			t.Fatal("a body using unsafe was offered for inlining")
		}
	}
}
