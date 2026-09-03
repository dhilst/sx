package main

import "testing"

func TestExtractJSONObject(t *testing.T) {
	cases := []struct {
		name  string
		reply string
		want  string
	}{
		{"bare", `{"candidates":[]}`, `{"candidates":[]}`},
		{"fenced", "```json\n{\"candidates\":[]}\n```", `{"candidates":[]}`},
		{"prose around", "Here you go:\n{\"candidates\":[]}\nHope that helps.", `{"candidates":[]}`},
		{"braces in string", `{"a":"}{","b":1}`, `{"a":"}{","b":1}`},
		{"escaped quote in string", `{"a":"x\"}","b":1}`, `{"a":"x\"}","b":1}`},
		{"nested", `{"a":{"b":{}}}`, `{"a":{"b":{}}}`},
		{"none", "sorry, no", ""},
		{"unbalanced", `{"a":1`, ""},
	}
	for _, c := range cases {
		if got := extractJSONObject(c.reply); got != c.want {
			t.Errorf("%s: extractJSONObject = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestSynthesizeCandidatesProducesApplicableDiff(t *testing.T) {
	files := []sourceFile{{Path: "pkg/f.go", Content: "package pkg\n\nfunc F() int {\n\tif true {\n\t\treturn 1\n\t}\n\treturn 0\n}\n"}}
	rewrites := []rewriteCandidate{{
		Description: "flatten F",
		Files:       []fileRewrite{{Path: "pkg/f.go", Content: "package pkg\n\nfunc F() int {\n\treturn 1\n}\n"}},
	}}
	got := synthesizeCandidates(rewrites, files)
	if len(got) != 1 {
		t.Fatalf("got %d candidates, want 1", len(got))
	}
	if got[0].Description != "flatten F" {
		t.Errorf("description = %q", got[0].Description)
	}
	for _, want := range []string{"--- a/pkg/f.go", "+++ b/pkg/f.go", "@@"} {
		if !contains(got[0].Patch, want) {
			t.Errorf("patch missing %q:\n%s", want, got[0].Patch)
		}
	}
}

func TestSynthesizeCandidatesRejectsUnofferedAndTestFiles(t *testing.T) {
	files := []sourceFile{{Path: "pkg/f.go", Content: "package pkg\n"}}
	rewrites := []rewriteCandidate{
		{Description: "touches tests", Files: []fileRewrite{{Path: "pkg/f_test.go", Content: "package pkg\n// gutted\n"}}},
		{Description: "touches unoffered file", Files: []fileRewrite{{Path: "other/g.go", Content: "package other\n"}}},
		{Description: "no-op rewrite", Files: []fileRewrite{{Path: "pkg/f.go", Content: "package pkg\n"}}},
		{Description: "empty content", Files: []fileRewrite{{Path: "pkg/f.go", Content: "   "}}},
	}
	if got := synthesizeCandidates(rewrites, files); len(got) != 0 {
		t.Fatalf("got %d candidates, want 0: %+v", len(got), got)
	}
}

func TestSynthesizeCandidatesDropsHunklessDiffs(t *testing.T) {
	// A NUL byte makes git treat the file as binary, so the diff carries no
	// hunks and `git apply` would reject it outright.
	files := []sourceFile{{Path: "pkg/f.go", Content: "package pkg\n"}}
	rewrites := []rewriteCandidate{{
		Description: "binary-looking rewrite",
		Files:       []fileRewrite{{Path: "pkg/f.go", Content: "package pkg\n\x00\n"}},
	}}
	if got := synthesizeCandidates(rewrites, files); len(got) != 0 {
		t.Fatalf("got %d candidates, want 0: %q", len(got), got[0].Patch)
	}
}

func TestParseRewritesCapsCandidates(t *testing.T) {
	reply := `{"candidates":[{"description":"a"},{"description":"b"},{"description":"c"},{"description":"d"}]}`
	got, err := parseRewrites(reply)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != maxCandidates {
		t.Fatalf("got %d candidates, want %d", len(got), maxCandidates)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

func TestCandidateBudgetShrinksWithInputSize(t *testing.T) {
	if got := candidateBudget(2 << 10); got != maxCandidates {
		t.Errorf("small input budget = %d, want %d", got, maxCandidates)
	}
	if got := candidateBudget(15 << 10); got != 2 {
		t.Errorf("medium input budget = %d, want 2", got)
	}
	if got := candidateBudget(40 << 10); got != 1 {
		t.Errorf("large input budget = %d, want 1", got)
	}
}
