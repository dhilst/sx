// Command sxprovider is a Claude-backed suggestion provider for
// `sx suggest --provider-command`.
//
// It reads a suggest.Context on stdin, asks the claude CLI for whole-file
// rewrites of the hotspot files, and synthesises the unified diffs itself with
// git. Providers never compute SC scores; every number in the context comes
// from sx and is passed through untouched.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"purgatrix/internal/suggest"
)

const (
	defaultMaxFiles   = 2
	maxFileBytes      = 48 << 10
	maxDiffBytes      = 24 << 10
	maxCandidates     = 3
	defaultModel      = "sonnet"
	defaultTimeoutSec = 600
)

// Whole-file rewrites are what make the synthesised diffs reliable, but they
// also mean the reply carries every line of every file it touches. Asking for
// three rewrites of a 700-line file is a reply the model cannot finish inside
// any sane timeout, so the number of candidates scales down as the input grows.
func candidateBudget(inputBytes int) int {
	switch {
	case inputBytes > 24<<10:
		return 1
	case inputBytes > 10<<10:
		return 2
	default:
		return maxCandidates
	}
}

func main() {
	if err := run(os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "sxprovider:", err)
		// A provider that cannot produce candidates is not a pipeline failure.
		fmt.Fprintln(os.Stdout, `{"candidates":[]}`)
		os.Exit(0)
	}
}

func run(stdin io.Reader, stdout, stderr io.Writer) error {
	raw, err := io.ReadAll(stdin)
	if err != nil {
		return err
	}
	var ctx suggest.Context
	if err := json.Unmarshal(raw, &ctx); err != nil {
		return fmt.Errorf("decode suggestion context: %w", err)
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	files, err := hotspotFiles(root, ctx)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		fmt.Fprintln(stdout, `{"candidates":[]}`)
		return nil
	}
	reply, err := askClaude(composeMessage(ctx, files), stderr)
	if err != nil {
		return err
	}
	rewrites, err := parseRewrites(reply)
	if err != nil {
		return err
	}
	candidates := synthesizeCandidates(rewrites, files)
	return json.NewEncoder(stdout).Encode(suggest.ProviderResponse{Candidates: candidates})
}

type sourceFile struct {
	Path    string
	Content string
}

// hotspotFiles reads the current head contents of the files sx flagged as
// hotspots. Test files are never offered for rewriting: a candidate that edits
// its own tests cannot demonstrate behaviour preservation.
func hotspotFiles(root string, ctx suggest.Context) ([]sourceFile, error) {
	var paths []string
	seen := map[string]bool{}
	for _, h := range ctx.Hotspots {
		if h.Path == "" || seen[h.Path] || strings.HasSuffix(h.Path, "_test.go") {
			continue
		}
		seen[h.Path] = true
		paths = append(paths, h.Path)
	}
	sort.SliceStable(paths, func(i, j int) bool { return contribution(ctx, paths[i]) > contribution(ctx, paths[j]) })

	// A caller that runs the provider repeatedly against an unchanged head gets
	// the same hotspot order every time, and so re-proposes the same rewrite.
	// Skipping the first N eligible files lets such a loop work down the list.
	if skip := envInt("SX_PROVIDER_SKIP_FILES", 0); skip > 0 && skip < len(paths) {
		paths = paths[skip:]
	}

	maxFiles := envInt("SX_PROVIDER_MAX_FILES", defaultMaxFiles)
	var files []sourceFile
	budget := maxFiles * maxFileBytes
	for _, path := range paths {
		if len(files) >= maxFiles {
			break
		}
		data, err := readHeadFile(root, ctx.HeadRef, path)
		if err != nil || len(data) > maxFileBytes || len(data) > budget {
			continue
		}
		budget -= len(data)
		files = append(files, sourceFile{Path: path, Content: string(data)})
	}
	return files, nil
}

// readHeadFile reads a file exactly as it exists at the head being scored, so
// the synthesised diff applies cleanly to sx's own checkout of that ref.
func readHeadFile(root, headRef, path string) ([]byte, error) {
	if headRef == "" || headRef == "WORKTREE" {
		return os.ReadFile(filepath.Join(root, path))
	}
	cmd := exec.Command("git", "show", headRef+":"+path)
	cmd.Dir = root
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git show %s:%s: %w\n%s", headRef, path, err, stderr.String())
	}
	return stdout.Bytes(), nil
}

func contribution(ctx suggest.Context, path string) int {
	total := 0
	for _, h := range ctx.Hotspots {
		if h.Path == path {
			total += h.Contribution
		}
	}
	return total
}

// composeMessage puts the evolvable prompt first and the fixed output contract
// last, so the contract wins over any conflicting instruction a prompt carries.
func composeMessage(ctx suggest.Context, files []sourceFile) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(ctx.Prompt))
	b.WriteString("\n\n=== MEASUREMENTS FROM sx (authoritative; never recompute or estimate) ===\n")
	fmt.Fprintf(&b, "base_ref=%s head_ref=%s\n", ctx.BaseRef, ctx.HeadRef)
	fmt.Fprintf(&b, "base_total=%d head_total=%d original_pr_score=%+d\n", ctx.BaseTotal, ctx.HeadTotal, ctx.OriginalPRScore)

	b.WriteString("\n=== HOTSPOTS (highest structural cost first) ===\n")
	for i, h := range ctx.Hotspots {
		if i >= 12 {
			break
		}
		fmt.Fprintf(&b, "%s:%d kind=%s contribution=%d\n", h.Path, h.StartLine, h.Kind, h.Contribution)
	}
	if len(ctx.Plans) > 0 {
		b.WriteString("\n=== OPPORTUNITIES sx ALREADY FOUND AND PRICED ===\n")
		b.WriteString("Each saving below was measured by applying the change to sx's graph and rescoring it.\n")
		b.WriteString("Prefer these over anything you find yourself; a plan with a blocker needs care.\n")
		for i, p := range ctx.Plans {
			if i >= 12 {
				break
			}
			saving := p.Saving
			how := "estimated"
			if p.Verified {
				saving = p.Exact
				how = "measured"
			}
			fmt.Fprintf(&b, "- %s at %s:%d saves %d (%s): %s\n", p.Kind, p.Path, p.StartLine, saving, how, p.Detail)
			for _, blocker := range p.Blockers {
				fmt.Fprintf(&b, "    caution: %s\n", blocker)
			}
		}
	}
	if len(ctx.Transformations) > 0 {
		b.WriteString("\n=== TRANSFORMATIONS sx REWARDS ===\n")
		for _, t := range ctx.Transformations {
			fmt.Fprintf(&b, "- %s\n", t)
		}
	}
	if len(ctx.AcceptanceCriteria) > 0 {
		b.WriteString("\n=== DETERMINISTIC ACCEPTANCE CRITERIA ===\n")
		for _, c := range ctx.AcceptanceCriteria {
			fmt.Fprintf(&b, "- %s\n", c)
		}
	}
	b.WriteString("\n=== CURRENT HEAD CONTENTS OF THE REWRITABLE FILES ===\n")
	for _, f := range files {
		fmt.Fprintf(&b, "--- FILE %s ---\n%s\n", f.Path, f.Content)
	}
	if diff := truncate(ctx.OriginalDiff, maxDiffBytes); diff != "" {
		fmt.Fprintf(&b, "\n=== ORIGINAL PR DIFF (may be truncated) ===\n%s\n", diff)
	}
	inputBytes := 0
	for _, f := range files {
		inputBytes += len(f.Content)
	}
	budget := candidateBudget(inputBytes)
	b.WriteString("\n=== OUTPUT CONTRACT (overrides any conflicting instruction above) ===\n")
	b.WriteString(`Reply with exactly one JSON object and no other text, no markdown fence, no commentary:
{"candidates":[{"description":"short imperative summary","files":[{"path":"<repo-relative path>","content":"<complete new text of the file>"}]}]}
Hard requirements:
- "content" is the COMPLETE new file text, not a diff, not a fragment, not an ellipsis.
- Only the file paths listed under CURRENT HEAD CONTENTS may appear. Never emit a _test.go file.
- Each candidate is applied and measured on its own, so it must be self-contained.
- Emit at most ` + strconv.Itoa(budget) + ` candidates, ordered best first.
- Preserve exported signatures, printed output, and observable behaviour exactly.
- If there is no credible simplification, reply {"candidates":[]}.`)
	return b.String()
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "\n... (truncated)"
}

func askClaude(message string, stderr io.Writer) (string, error) {
	model := envOr("SX_PROVIDER_MODEL", defaultModel)
	timeout := time.Duration(envInt("SX_PROVIDER_TIMEOUT_SEC", defaultTimeoutSec)) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "claude", "-p", "--model", model, "--allowedTools", "", "--output-format", "text")
	cmd.Stdin = strings.NewReader(message)
	var stdout, errBuf bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("claude -p: %w\n%s", err, truncate(errBuf.String(), 2000))
	}
	if errBuf.Len() > 0 {
		fmt.Fprintln(stderr, "sxprovider: claude stderr:", truncate(errBuf.String(), 500))
	}
	return stdout.String(), nil
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key))); err == nil && n > 0 {
		return n
	}
	return fallback
}

type fileRewrite struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type rewriteCandidate struct {
	Description string        `json:"description"`
	Files       []fileRewrite `json:"files"`
}

type rewriteResponse struct {
	Candidates []rewriteCandidate `json:"candidates"`
}

func parseRewrites(reply string) ([]rewriteCandidate, error) {
	text := extractJSONObject(reply)
	if text == "" {
		return nil, fmt.Errorf("no JSON object in provider reply")
	}
	var response rewriteResponse
	if err := json.Unmarshal([]byte(text), &response); err != nil {
		return nil, fmt.Errorf("decode provider reply: %w", err)
	}
	if len(response.Candidates) > maxCandidates {
		response.Candidates = response.Candidates[:maxCandidates]
	}
	return response.Candidates, nil
}

// extractJSONObject pulls the outermost balanced JSON object out of a reply,
// tolerating markdown fences and stray prose around it.
func extractJSONObject(reply string) string {
	text := strings.TrimSpace(reply)
	if fence := strings.Index(text, "```"); fence >= 0 {
		rest := text[fence+3:]
		if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
			rest = rest[nl+1:]
		}
		if end := strings.Index(rest, "```"); end >= 0 {
			rest = rest[:end]
		}
		text = strings.TrimSpace(rest)
	}
	start := strings.IndexByte(text, '{')
	if start < 0 {
		return ""
	}
	depth, inString, escaped := 0, false, false
	for i := start; i < len(text); i++ {
		c := text[i]
		switch {
		case escaped:
			escaped = false
		case c == '\\' && inString:
			escaped = true
		case c == '"':
			inString = !inString
		case inString:
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return text[start : i+1]
			}
		}
	}
	return ""
}

// synthesizeCandidates turns whole-file rewrites into unified diffs that
// `git apply -p1` accepts from the repository root.
func synthesizeCandidates(rewrites []rewriteCandidate, files []sourceFile) []suggest.CandidatePatch {
	originals := map[string]string{}
	for _, f := range files {
		originals[f.Path] = f.Content
	}
	candidates := []suggest.CandidatePatch{}
	for i, rc := range rewrites {
		var patch strings.Builder
		for _, fr := range rc.Files {
			original, ok := originals[fr.Path]
			if !ok || strings.HasSuffix(fr.Path, "_test.go") {
				continue
			}
			content := normalizeTrailingNewline(fr.Content)
			if content == original || strings.TrimSpace(content) == "" {
				continue
			}
			diff, err := unifiedDiff(fr.Path, original, content)
			if err != nil || !strings.Contains(diff, "\n@@") {
				// git renders some differences without hunks (a binary-detected
				// file, a mode-only change). `git apply` rejects those with "no
				// valid patches in input", which would cost the prompt fitness
				// for a defect in this provider rather than in its suggestion.
				continue
			}
			patch.WriteString(diff)
		}
		if patch.Len() == 0 {
			continue
		}
		candidates = append(candidates, suggest.CandidatePatch{
			Description: describe(rc.Description, i),
			Patch:       patch.String(),
		})
	}
	return candidates
}

func describe(description string, index int) string {
	if d := strings.TrimSpace(description); d != "" {
		return d
	}
	return fmt.Sprintf("provider candidate %d", index+1)
}

func normalizeTrailingNewline(content string) string {
	if content == "" || strings.HasSuffix(content, "\n") {
		return content
	}
	return content + "\n"
}

func unifiedDiff(path, original, updated string) (string, error) {
	dir, err := os.MkdirTemp("", "sxprovider-diff-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	oldPath := filepath.Join(dir, "a", path)
	newPath := filepath.Join(dir, "b", path)
	if err := writeFile(oldPath, original); err != nil {
		return "", err
	}
	if err := writeFile(newPath, updated); err != nil {
		return "", err
	}
	cmd := exec.Command("git", "diff", "--no-index", "--no-prefix", "--", filepath.Join("a", path), filepath.Join("b", path))
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// git diff exits 1 when the inputs differ, which is the expected case here.
	if err := cmd.Run(); err != nil && stdout.Len() == 0 {
		return "", fmt.Errorf("git diff --no-index: %w\n%s", err, stderr.String())
	}
	return stdout.String(), nil
}

func writeFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}
