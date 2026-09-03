package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"purgatrix/internal/sc"
)

func TestAnalyzePRUsesGitRefs(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "sx@example.invalid")
	runGit(t, dir, "config", "user.name", "sx test")
	runGit(t, dir, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module sample\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sample.go"), []byte("package sample\n\nfunc f() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "base")
	if err := os.WriteFile(filepath.Join(dir, "sample.go"), []byte("package sample\n\nfunc f(a int) int { if a > 0 { return a }; return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "head")

	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	report, err := analyzePR("HEAD~1", "HEAD", nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Score <= 0 {
		t.Fatalf("expected head to increase SC, got %+d", report.Score)
	}
}

func TestParseProviderCandidates(t *testing.T) {
	cases := []struct {
		name      string
		output    string
		wantCount int
		wantPatch string
	}{
		{"declined", `{"candidates":[]}`, 0, ""},
		{"empty object", `{}`, 0, ""},
		{"empty output", "   ", 0, ""},
		{"candidate list", `{"candidates":[{"description":"d","patch":"--- a/x\n"}]}`, 1, "--- a/x\n"},
		{"single patch field", `{"description":"d","patch":"--- a/x\n"}`, 1, "--- a/x\n"},
		{"raw diff", "--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n", 1, "--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseProviderCandidates([]byte(c.output))
			if len(got) != c.wantCount {
				t.Fatalf("got %d candidates, want %d: %+v", len(got), c.wantCount, got)
			}
			if c.wantCount == 1 && got[0].Patch != c.wantPatch {
				t.Errorf("patch = %q, want %q", got[0].Patch, c.wantPatch)
			}
		})
	}
}

// A provider that returns a malformed or stale patch must cost that candidate
// only, not the whole suggestion run.
func TestValidateCandidateRejectsInsteadOfFailing(t *testing.T) {
	dir := sampleRepo(t)
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	pr, err := analyzePR("HEAD~1", "HEAD", nil)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name       string
		patch      string
		wantReason string
	}{
		{"empty", "   \n", "candidate patch was empty"},
		{"not a diff", "this is not a patch at all\n", "did not apply"},
		{"stale hunk", "--- a/sample.go\n+++ b/sample.go\n@@ -1,1 +1,1 @@\n-package nothing\n+package other\n", "did not apply"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			outcome, err := validateCandidate("HEAD", CandidatePatch{Description: c.name, Patch: c.patch}, "true", pr, nil)
			if err != nil {
				t.Fatalf("validateCandidate returned an error instead of a rejection: %v", err)
			}
			if outcome.Accepted {
				t.Error("candidate was accepted")
			}
			if outcome.Applied {
				t.Error("outcome claims the patch applied")
			}
			if !strings.Contains(outcome.Reason, c.wantReason) {
				t.Errorf("reason = %q, want it to mention %q", outcome.Reason, c.wantReason)
			}
		})
	}
}

func sampleRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "sx@example.invalid")
	runGit(t, dir, "config", "user.name", "sx test")
	runGit(t, dir, "config", "commit.gpgsign", "false")
	write(t, dir, "go.mod", "module sample\n\ngo 1.25\n")
	write(t, dir, "sample.go", "package sample\n\nfunc f() int { return 1 }\n")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "base")
	write(t, dir, "sample.go", "package sample\n\nfunc f(a int) int { if a > 0 { return a }; return 1 }\n")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "head")
	return dir
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, string(out))
	}
}

func TestParseIgnoreRejectsBadPatterns(t *testing.T) {
	if _, err := parseIgnore("internal/*, *_gen.go , ,docs/"); err != nil {
		t.Fatalf("valid patterns rejected: %v", err)
	}
	if _, err := parseIgnore("["); err == nil {
		t.Error("expected a malformed glob to be rejected")
	}
}

func TestIgnoreGlobsMatch(t *testing.T) {
	globs, err := parseIgnore("internal/legacy,*_gen.go,cmd/*/vendorish")
	if err != nil {
		t.Fatal(err)
	}
	ignored := []string{
		"internal/legacy",              // the directory itself
		"internal/legacy/deep/file.go", // anything beneath it
		"api_gen.go",                   // base-name glob
		"internal/api/api_gen.go",      // base-name glob at depth
		"cmd/sx/vendorish",             // wildcard segment
	}
	for _, rel := range ignored {
		if !globs.match(rel) {
			t.Errorf("%q should be ignored", rel)
		}
	}
	kept := []string{"internal/legacyish/file.go", "cmd/sx/main.go", "generated.go", "internal/sc/report.go"}
	for _, rel := range kept {
		if globs.match(rel) {
			t.Errorf("%q should not be ignored", rel)
		}
	}
	if (ignoreGlobs(nil)).match("anything.go") {
		t.Error("an empty ignore list must ignore nothing")
	}
}

func TestCompileRepoHonoursIgnore(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module sample\n\ngo 1.25\n")
	write(t, dir, "keep.go", "package sample\n\nfunc Keep(a int) int { if a > 0 { return a }; return 0 }\n")
	if err := os.MkdirAll(filepath.Join(dir, "skipme"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "skipme/drop.go", "package skipme\n\nfunc Drop(a int) int { if a > 0 { return a }; return 0 }\n")

	full, err := compileRepo(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	trimmed, err := compileRepo(dir, ignoreGlobs{"skipme"})
	if err != nil {
		t.Fatal(err)
	}
	fullScore, err := sc.Complexity(full)
	if err != nil {
		t.Fatal(err)
	}
	trimmedScore, err := sc.Complexity(trimmed)
	if err != nil {
		t.Fatal(err)
	}
	if !(trimmedScore.Total < fullScore.Total) {
		t.Fatalf("ignoring a package did not lower the score: %d vs %d", trimmedScore.Total, fullScore.Total)
	}
	for _, f := range trimmedScore.Files {
		if strings.HasPrefix(f.Path, "skipme") {
			t.Errorf("ignored path %q still scored", f.Path)
		}
	}
}
