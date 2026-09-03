// Package ga runs a genetic algorithm over sx suggestion prompts.
//
// Fitness is measured, never estimated: every candidate a prompt produces is
// pushed through the real `sx suggest` acceptance pipeline (apply, tests,
// deterministic behaviour test, measured SC reduction) against a corpus of
// small git-backed Go cases with known simplification headroom.
package ga

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

//go:embed all:testdata
var caseFS embed.FS

// CorpusCases is the corpus the optimizer evolves against. CorpusHoldout is
// kept out of evolution so a winning prompt can be checked on structure it was
// never bred to handle.
const (
	CorpusCases   = "testdata/cases"
	CorpusHoldout = "testdata/holdout"
	CorpusHard    = "testdata/hard"
)

// Case is one evaluation repository: a base commit, a head commit whose new
// code carries avoidable structural complexity, and a reference simplification
// that establishes the attainable ceiling.
type Case struct {
	Name       string `json:"name"`
	Summary    string `json:"summary"`
	DBTCommand string `json:"dbt_command"`

	// Ceiling is the SC reduction achieved by the reference simplification.
	// It is measured at startup, not assumed.
	Ceiling int `json:"ceiling"`

	// Corpus is the embedded directory the case was loaded from.
	Corpus string `json:"corpus"`
}

// LoadCases reads the embedded case definitions from one corpus.
func LoadCases(corpus string) ([]Case, error) {
	entries, err := fs.ReadDir(caseFS, corpus)
	if err != nil {
		return nil, err
	}
	var cases []Case
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		data, err := caseFS.ReadFile(filepath.Join(corpus, e.Name(), "case.json"))
		if err != nil {
			return nil, err
		}
		var c Case
		if err := json.Unmarshal(data, &c); err != nil {
			return nil, fmt.Errorf("%s/case.json: %w", e.Name(), err)
		}
		if c.Name == "" {
			c.Name = e.Name()
		}
		c.Corpus = corpus
		cases = append(cases, c)
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("no evaluation cases embedded under %s", corpus)
	}
	return cases, nil
}

// Materialize writes a fresh git repository for the case into dir: the base
// tree as the first commit and the head tree as the second, so that base is
// HEAD~1 and head is HEAD.
func Materialize(c Case, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := copyTree(filepath.Join(c.Corpus, c.Name, "base"), dir); err != nil {
		return err
	}
	if err := git(dir, "init", "-q"); err != nil {
		return err
	}
	if err := commitAll(dir, "base"); err != nil {
		return err
	}
	if err := copyTree(filepath.Join(c.Corpus, c.Name, "head"), dir); err != nil {
		return err
	}
	return commitAll(dir, "head")
}

// ReferencePatch renders the case's reference simplification as a unified diff
// against the head tree.
func ReferencePatch(c Case, dir string) (string, error) {
	if err := copyTree(filepath.Join(c.Corpus, c.Name, "reference"), dir); err != nil {
		return "", err
	}
	patch, err := gitOutput(dir, "diff")
	if err != nil {
		return "", err
	}
	if err := git(dir, "checkout", "--", "."); err != nil {
		return "", err
	}
	if strings.TrimSpace(patch) == "" {
		return "", fmt.Errorf("case %s: reference tree is identical to head", c.Name)
	}
	return patch, nil
}

func copyTree(srcPrefix, dstDir string) error {
	return fs.WalkDir(caseFS, srcPrefix, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcPrefix, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dstDir, materializedName(rel))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := caseFS.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// materializedName maps template names back to their real filenames. A case's
// go.mod is stored as go.mod.txt because a directory holding a go.mod is a
// nested module, which the go tool excludes from this module's embedded files.
func materializedName(rel string) string {
	if strings.HasSuffix(rel, "go.mod.txt") {
		return strings.TrimSuffix(rel, ".txt")
	}
	return rel
}

func commitAll(dir, message string) error {
	if err := git(dir, "add", "-A"); err != nil {
		return err
	}
	return git(dir, "commit", "-q", "-m", message)
}

// gitArgs pins identity and disables signing so evaluation repositories do not
// depend on the developer's global git configuration.
func gitArgs(args ...string) []string {
	pinned := []string{
		"-c", "user.name=sxga",
		"-c", "user.email=sxga@invalid",
		"-c", "commit.gpgsign=false",
		"-c", "tag.gpgsign=false",
		"-c", "core.hooksPath=/dev/null",
	}
	return append(pinned, args...)
}

func git(dir string, args ...string) error {
	_, err := gitOutput(dir, args...)
	return err
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", gitArgs(args...)...)
	cmd.Dir = dir
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return stdout.String(), nil
}
