package main

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/build"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"purgatrix/internal/analyze"
	"purgatrix/internal/cache"
	gofront "purgatrix/internal/frontend/golang"
	"purgatrix/internal/sc"
	"purgatrix/internal/suggest"
	"purgatrix/internal/sx"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "sx:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		usage(stderr)
		return fmt.Errorf("missing command")
	}
	switch args[0] {
	case "compile":
		return cmdCompile(args[1:], stdout)
	case "score":
		return cmdScore(args[1:], stdout)
	case "repo":
		return cmdRepo(args[1:], stdout)
	case "pr":
		return cmdPR(args[1:], stdout)
	case "analyze":
		return cmdAnalyze(args[1:], stdout)
	case "suggest":
		return cmdSuggest(args[1:], stdout)
	default:
		usage(stderr)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "usage: sx <compile|score|repo|pr|analyze|suggest> [args]")
}

func cmdCompile(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("compile", flag.ContinueOnError)
	outPath := fs.String("o", "", "write .sx to file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("compile expects one .go file")
	}
	path := fs.Arg(0)
	root, _ := os.Getwd()
	g, err := gofront.CompileFile(path, root)
	if err != nil {
		return err
	}
	if *outPath != "" {
		f, err := os.Create(*outPath)
		if err != nil {
			return err
		}
		defer f.Close()
		return sx.Encode(f, g)
	}
	return sx.Encode(stdout, g)
}

func cmdScore(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("score", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit JSON report")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("score expects one .go or .sx file")
	}
	g, err := graphForPath(fs.Arg(0))
	if err != nil {
		return err
	}
	return writeScore(stdout, g, *jsonOut)
}

func cmdRepo(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("repo", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit JSON report")
	ignoreCSV := fs.String("ignore", "", "comma-separated globs to leave out of the score, e.g. \"internal/legacy,*_gen.go\"")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ignore, err := parseIgnore(*ignoreCSV)
	if err != nil {
		return err
	}
	dir := "."
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
	}
	g, err := compileRepo(dir, ignore)
	if err != nil {
		return err
	}
	return writeScore(stdout, g, *jsonOut)
}

func graphForPath(path string) (*sx.Graph, error) {
	if strings.HasSuffix(path, ".sx") || strings.HasSuffix(path, ".json") {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		g, err := sx.Decode(f)
		if err != nil {
			return nil, err
		}
		c := sx.Canonical(g)
		return &c, sx.Validate(&c)
	}
	root, _ := os.Getwd()
	return gofront.CompileFile(path, root)
}

func compileRepo(dir string, ignore ignoreGlobs) (*sx.Graph, error) {
	return compileRepoWithCache(dir, "", ignore)
}

func compileRepoWithCache(dir, cacheDir string, ignore ignoreGlobs) (*sx.Graph, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if cacheDir == "" {
		cacheDir = cache.DefaultDir(root)
	}
	c := cache.New(cacheDir)
	var graphs []*sx.Graph
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			rel = p
		}
		if d.IsDir() {
			if skipDir(d.Name(), p, root) || (rel != "." && ignore.match(rel)) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") && !ignore.match(rel) {
			// Respect build constraints. A package with per-platform files
			// declares the same functions in each of them; compiling all of
			// them merges several definitions of one identity and fails.
			if ok, err := buildContext.MatchFile(filepath.Dir(p), filepath.Base(p)); err != nil || !ok {
				return nil
			}
			g, _, err := c.CompileGoFile(p, root)
			if err != nil {
				return err
			}
			graphs = append(graphs, g)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return sx.Merge(graphs...)
}

// buildContext decides which files belong to the build for this platform.
// Scores are therefore platform-specific, exactly as the compiled program is.
var buildContext = build.Default

// skipDir reports whether a directory holds something other than the program
// being scored. It follows the go tool's own rules: directories beginning with
// "." or "_" are invisible to the build, vendor is vendored dependencies, and
// testdata is fixtures. Scoring any of them charges a repository for code it
// does not ship.
func skipDir(name, path, root string) bool {
	if path == root {
		return false
	}
	switch name {
	case "vendor", "testdata":
		return true
	}
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

// ignoreGlobs is a caller-supplied list of paths to leave out of a repository
// score, on top of the directories skipDir always excludes.
type ignoreGlobs []string

// parseIgnore reads a comma-separated glob list and rejects malformed patterns
// up front, so a typo surfaces as an error instead of silently scoring
// everything.
func parseIgnore(csv string) (ignoreGlobs, error) {
	var globs ignoreGlobs
	for _, raw := range strings.Split(csv, ",") {
		pattern := strings.TrimSpace(raw)
		if pattern == "" {
			continue
		}
		if _, err := path.Match(pattern, "probe"); err != nil {
			return nil, fmt.Errorf("bad --ignore pattern %q: %w", pattern, err)
		}
		globs = append(globs, strings.TrimSuffix(pattern, "/"))
	}
	return globs, nil
}

// match reports whether a repository-relative path is ignored. A pattern is
// tried against the whole path and against the final element, and a pattern
// naming a directory ignores everything beneath it.
func (g ignoreGlobs) match(rel string) bool {
	rel = filepath.ToSlash(rel)
	base := path.Base(rel)
	for _, pattern := range g {
		if ok, _ := path.Match(pattern, rel); ok {
			return true
		}
		if ok, _ := path.Match(pattern, base); ok {
			return true
		}
		if rel == pattern || strings.HasPrefix(rel, pattern+"/") {
			return true
		}
	}
	return false
}

func writeScore(stdout io.Writer, g *sx.Graph, jsonOut bool) error {
	r, err := sc.Complexity(g)
	if err != nil {
		return err
	}
	if jsonOut {
		return writeJSON(stdout, r)
	}
	fmt.Fprint(stdout, sc.FormatText(r))
	return nil
}

// cmdAnalyze reports what the graph says can be simplified, and what sx
// computes each opportunity is worth, without running a provider.
func cmdAnalyze(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("analyze", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	ignoreCSV := fs.String("ignore", "", "comma-separated globs to leave out")
	limit := fs.Int("n", 20, "how many plans to print")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ignore, err := parseIgnore(*ignoreCSV)
	if err != nil {
		return err
	}
	target := "."
	if fs.NArg() > 0 {
		target = fs.Arg(0)
	}
	var g *sx.Graph
	if strings.HasSuffix(target, ".go") || strings.HasSuffix(target, ".sx") {
		g, err = graphForPath(target)
	} else {
		g, err = compileRepo(target, ignore)
	}
	if err != nil {
		return err
	}
	report, err := analyze.Analyze(g)
	if err != nil {
		return err
	}
	if *jsonOut {
		return writeJSON(stdout, report)
	}
	fmt.Fprintf(stdout, "%d plans, %d SC if every one landed\n", len(report.Plans), report.Total)
	fmt.Fprintf(stdout, "%d of them do not overlap and carry no blocker: %d SC\n\n", len(report.Selected), report.Selection)
	for i, p := range report.Plans {
		if i >= *limit {
			fmt.Fprintf(stdout, "... %d more\n", len(report.Plans)-*limit)
			break
		}
		mark := "estimated"
		if p.Verified {
			mark = "measured"
		}
		fmt.Fprintf(stdout, "%-16s %-38s -%-5d %s\n    %s\n", p.Kind, fmt.Sprintf("%s:%d", p.Path, p.StartLine), p.Best(), mark, p.Detail)
		for _, b := range p.Blockers {
			fmt.Fprintf(stdout, "    blocked: %s\n", b)
		}
	}
	return nil
}

func cmdPR(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("pr", flag.ContinueOnError)
	base := fs.String("base", "", "base git ref")
	head := fs.String("head", "HEAD", "head git ref or WORKTREE")
	jsonOut := fs.Bool("json", false, "emit JSON")
	ignoreCSV := fs.String("ignore", "", "comma-separated globs to leave out of the score")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *base == "" {
		return fmt.Errorf("pr requires --base")
	}
	ignore, err := parseIgnore(*ignoreCSV)
	if err != nil {
		return err
	}
	result, err := analyzePR(*base, *head, ignore)
	if err != nil {
		return err
	}
	if *jsonOut {
		return writeJSON(stdout, result)
	}
	fmt.Fprintf(stdout, "Base SC: %d\nHead SC: %d\nOriginal PR SC: %+d\n", result.Base.Total, result.Head.Total, result.Score)
	return nil
}

func cmdSuggest(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("suggest", flag.ContinueOnError)
	base := fs.String("base", "", "base git ref")
	head := fs.String("head", "HEAD", "head git ref or WORKTREE")
	candidate := fs.String("candidate", "", "candidate patch file to validate")
	provider := fs.String("provider-command", "", "external command that reads suggestion context JSON on stdin and emits a unified diff on stdout")
	promptFile := fs.String("prompt-file", "", "file holding the suggestion prompt sent to the provider (defaults to the built-in prompt)")
	testCmd := fs.String("test", "go test ./...", "test command")
	patchDir := fs.String("write-patches", "", "directory to write each accepted simplification to as an applicable patch")
	ignoreCSV := fs.String("ignore", "", "comma-separated globs to leave out of the score and out of suggestions")
	jsonOut := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *base == "" {
		return fmt.Errorf("suggest requires --base")
	}
	prompt, err := loadPrompt(*promptFile)
	if err != nil {
		return err
	}
	ignore, err := parseIgnore(*ignoreCSV)
	if err != nil {
		return err
	}
	pr, err := analyzePR(*base, *head, ignore)
	if err != nil {
		return err
	}
	out := SuggestReport{
		OriginalPRScore: pr.Score,
		Accepted:        []AcceptedSuggestion{},
	}
	if *candidate != "" {
		patch, err := os.ReadFile(*candidate)
		if err != nil {
			return err
		}
		candidatePatch := CandidatePatch{Description: "validated candidate patch", Patch: string(patch)}
		accepted, err := validateCandidate(*head, candidatePatch, *testCmd, pr, ignore)
		if err != nil {
			return err
		}
		out.Add(accepted)
	} else if *provider != "" {
		candidates, err := generateCandidatePatches(*provider, *base, *head, prompt, pr)
		if err != nil {
			return err
		}
		for _, candidatePatch := range candidates {
			accepted, err := validateCandidate(*head, candidatePatch, *testCmd, pr, ignore)
			if err != nil {
				return err
			}
			out.Add(accepted)
		}
	}
	written, err := writeAcceptedPatches(*patchDir, out)
	if err != nil {
		return err
	}
	if *jsonOut {
		return writeJSON(stdout, out)
	}
	writeSuggestText(stdout, out, *candidate == "" && *provider == "")
	for _, path := range written {
		fmt.Fprintf(stdout, "\nwrote %s\n", path)
	}
	return nil
}

// writeAcceptedPatches saves each accepted simplification so it can be applied
// with `git apply`. sx validates candidates in throwaway checkouts, so without
// this the patch that earned the score is discarded with them.
func writeAcceptedPatches(dir string, out SuggestReport) ([]string, error) {
	if dir == "" {
		return nil, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	var written []string
	for i, s := range out.Accepted {
		if s.Patch == "" {
			continue
		}
		path := filepath.Join(dir, fmt.Sprintf("accepted-%02d.patch", i+1))
		if err := os.WriteFile(path, []byte(s.Patch), 0o644); err != nil {
			return nil, err
		}
		written = append(written, path)
	}
	return written, nil
}

func writeJSON(stdout io.Writer, v any) error {
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func writeSuggestText(stdout io.Writer, out SuggestReport, noProvider bool) {
	fmt.Fprintf(stdout, "Original PR SC: %+d\n\nAccepted simplifications:\n", out.OriginalPRScore)
	if len(out.Accepted) == 0 {
		fmt.Fprintln(stdout, "none")
		if noProvider {
			fmt.Fprintln(stdout, "\nNo candidate provider is configured. Pass --candidate <patch> or --provider-command <cmd>.")
		}
		return
	}
	for i, s := range out.Accepted {
		fmt.Fprintf(stdout, "\n%d. %s\n   SC improvement: %+d\n   DBT: %s\n", i+1, s.Description, s.Improvement, s.DBTResult)
	}
}

type PRReport struct {
	Base  sc.Report `json:"base"`
	Head  sc.Report `json:"head"`
	Score int       `json:"score"`
}

type SuggestReport = suggest.Report

type AcceptedSuggestion = suggest.Outcome

type CandidatePatch = suggest.CandidatePatch

type SuggestionContext = suggest.Context

type SourceSnippet = suggest.SourceSnippet

func analyzePR(baseRef, headRef string, ignore ignoreGlobs) (PRReport, error) {
	baseDir, err := checkoutRef(baseRef)
	if err != nil {
		return PRReport{}, err
	}
	defer os.RemoveAll(baseDir)
	headDir := ""
	if headRef == "WORKTREE" {
		headDir, err = os.Getwd()
		if err != nil {
			return PRReport{}, err
		}
	} else {
		headDir, err = checkoutRef(headRef)
		if err != nil {
			return PRReport{}, err
		}
		defer os.RemoveAll(headDir)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return PRReport{}, err
	}
	sharedCacheDir := cache.DefaultDir(cwd)
	baseGraph, err := compileRepoWithCache(baseDir, sharedCacheDir, ignore)
	if err != nil {
		return PRReport{}, err
	}
	headGraph, err := compileRepoWithCache(headDir, sharedCacheDir, ignore)
	if err != nil {
		return PRReport{}, err
	}
	baseScore, err := sc.Complexity(baseGraph)
	if err != nil {
		return PRReport{}, err
	}
	headScore, err := sc.Complexity(headGraph)
	if err != nil {
		return PRReport{}, err
	}
	return PRReport{Base: baseScore, Head: headScore, Score: headScore.Total - baseScore.Total}, nil
}

func validateCandidate(headRef string, candidate CandidatePatch, testCmd string, original PRReport, ignore ignoreGlobs) (AcceptedSuggestion, error) {
	originalDir, err := checkoutRef(headRef)
	if err != nil {
		return AcceptedSuggestion{}, err
	}
	defer os.RemoveAll(originalDir)
	candidateDir, err := checkoutRef(headRef)
	if err != nil {
		return AcceptedSuggestion{}, err
	}
	defer os.RemoveAll(candidateDir)
	if err := runIn(candidateDir, "git", "init", "-q"); err != nil {
		return AcceptedSuggestion{}, err
	}
	if strings.TrimSpace(candidate.Patch) == "" {
		return rejected(candidate, original, "candidate patch was empty"), nil
	}
	apply := exec.Command("git", "apply", "--whitespace=nowarn", "-")
	apply.Dir = candidateDir
	apply.Stdin = strings.NewReader(candidate.Patch)
	if out, err := apply.CombinedOutput(); err != nil {
		return rejected(candidate, original, fmt.Sprintf("candidate patch did not apply: %s", firstLine(string(out)))), nil
	}

	origRun := runShell(originalDir, testCmd)
	candRun := runShell(candidateDir, testCmd)
	tests := []string{testCmd}
	dbt := "PASS"
	reason := ""
	testsPassed := origRun.Success && candRun.Success
	dbtMatch := origRun.Stdout == candRun.Stdout && origRun.Stderr == candRun.Stderr && origRun.Code == candRun.Code
	if !testsPassed {
		dbt = "FAIL"
		reason = "tests failed"
	} else if !dbtMatch {
		dbt = "FAIL"
		reason = "observable command output differed"
	}
	cwd, err := os.Getwd()
	if err != nil {
		return AcceptedSuggestion{}, err
	}
	result := rejected(candidate, original, reason)
	result.Applied = true
	result.TestsPassed = testsPassed
	result.DBTMatch = dbtMatch
	result.DBTResult = dbt
	result.TestsExecuted = tests
	candidateGraph, err := compileRepoWithCache(candidateDir, cache.DefaultDir(cwd), ignore)
	if err != nil {
		result.Reason = fmt.Sprintf("candidate did not translate to .sx: %s", firstLine(err.Error()))
		return result, nil
	}
	candidateScore, err := sc.Complexity(candidateGraph)
	if err != nil {
		result.Reason = fmt.Sprintf("candidate did not score: %s", firstLine(err.Error()))
		return result, nil
	}
	result.Scored = true
	result.CandidatePRScore = candidateScore.Total - original.Base.Total
	result.Improvement = result.CandidatePRScore - original.Score
	result.Accepted = dbt == "PASS" && result.CandidatePRScore < original.Score
	if result.Accepted {
		result.Reason = ""
		result.Patch = candidate.Patch
	} else if result.Reason == "" {
		result.Reason = "candidate did not reduce measured SC"
	}
	return result, nil
}

func rejected(candidate CandidatePatch, original PRReport, reason string) AcceptedSuggestion {
	return AcceptedSuggestion{
		Description:     candidate.Description,
		Accepted:        false,
		Reason:          reason,
		OriginalPRScore: original.Score,
		DBTResult:       "NOT_RUN",
	}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

func loadPrompt(path string) (string, error) {
	if path == "" {
		return suggestionPrompt(), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	prompt := strings.TrimSpace(string(data))
	if prompt == "" {
		return "", fmt.Errorf("prompt file %s is empty", path)
	}
	return prompt, nil
}

func generateCandidatePatches(providerCommand, baseRef, headRef, prompt string, pr PRReport) ([]CandidatePatch, error) {
	diff, err := gitDiff(baseRef, headRef)
	if err != nil {
		return nil, err
	}
	snippets, err := hotspotSourceSnippets(headRef, pr.Head.Hotspots)
	if err != nil {
		return nil, err
	}
	ctx := SuggestionContext{
		BaseRef:         baseRef,
		HeadRef:         headRef,
		OriginalPRScore: pr.Score,
		BaseTotal:       pr.Base.Total,
		HeadTotal:       pr.Head.Total,
		HeadRoots:       pr.Head.Roots,
		Hotspots:        pr.Head.Hotspots,
		HotspotSources:  snippets,
		OriginalDiff:    diff,
		Prompt:          prompt,
		Transformations: []string{
			"flatten control flow",
			"reduce repeated branching",
			"localize state",
			"remove wrapper logic that does not carry policy",
			"consolidate repeated error handling only when it lowers measured SC",
			"prefer small independent diffs over broad rewrites",
		},
		AcceptanceCriteria: []string{
			"build and configured tests pass",
			"DBT observes identical exit code, stdout, and stderr",
			"candidate PR score is lower than original PR score",
			"SC scores must be produced only by sx, never estimated",
		},
		OutputFormat: "Either a raw unified diff, or JSON: {\"candidates\":[{\"description\":\"...\",\"patch\":\"<unified diff>\"}]}",
		Instruction:  "Generate behavior-preserving simplification candidates for the hotspots. Do not include commentary in raw diff mode. Never fabricate, estimate, or manually modify SC scores.",
	}
	input, err := json.MarshalIndent(ctx, "", "  ")
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("sh", "-c", providerCommand)
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("provider command failed: %w\n%s", err, stderr.String())
	}
	return parseProviderCandidates(stdout.Bytes()), nil
}

func suggestionPrompt() string {
	return suggest.DefaultPrompt()
}

func parseProviderCandidates(output []byte) []CandidatePatch {
	text := strings.TrimSpace(string(output))
	if text == "" {
		return nil
	}
	// A unified diff is never valid JSON, so a successful decode means the
	// provider answered in the structured form and its answer stands as given.
	// An explicit {"candidates":[]} means "no credible simplification"; treating
	// it as a raw diff would manufacture a patch that cannot apply and blame the
	// provider for declining.
	var response suggest.ProviderResponse
	if json.Unmarshal([]byte(text), &response) == nil {
		if len(response.Candidates) > 0 {
			return response.Candidates
		}
		if response.Patch != "" {
			return []CandidatePatch{{Description: response.Description, Patch: response.Patch}}
		}
		return nil
	}
	// git apply rejects a patch whose final line was stripped of its newline.
	return []CandidatePatch{{Description: "provider candidate patch", Patch: text + "\n"}}
}

func hotspotSourceSnippets(headRef string, hotspots []sc.Hotspot) ([]SourceSnippet, error) {
	dir, cleanup, err := sourceDirForRef(headRef)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	seen := map[string]bool{}
	var snippets []SourceSnippet
	for _, h := range hotspots {
		key := fmt.Sprintf("%s:%d", h.Path, h.StartLine)
		if seen[key] || len(snippets) >= 8 {
			continue
		}
		seen[key] = true
		snippet, err := readSnippet(filepath.Join(dir, h.Path), h.StartLine, 5)
		if err != nil {
			continue
		}
		snippet.Path = h.Path
		snippets = append(snippets, snippet)
	}
	return snippets, nil
}

func sourceDirForRef(ref string) (string, func(), error) {
	if ref == "WORKTREE" {
		dir, err := os.Getwd()
		return dir, func() {}, err
	}
	dir, err := checkoutRef(ref)
	if err != nil {
		return "", func() {}, err
	}
	return dir, func() { os.RemoveAll(dir) }, nil
}

func readSnippet(path string, center, radius int) (SourceSnippet, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SourceSnippet{}, err
	}
	lines := strings.Split(string(data), "\n")
	start := max(1, center-radius)
	end := min(len(lines), center+radius)
	var b strings.Builder
	for i := start; i <= end; i++ {
		fmt.Fprintf(&b, "%4d: %s\n", i, lines[i-1])
	}
	return SourceSnippet{StartLine: start, EndLine: end, Text: b.String()}, nil
}

func gitDiff(baseRef, headRef string) (string, error) {
	args := []string{"diff", "--no-ext-diff", baseRef}
	if headRef != "WORKTREE" {
		args = append(args, headRef)
	}
	cmd := exec.Command("git", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return stdout.String(), nil
}

type commandResult struct {
	Code    int
	Stdout  string
	Stderr  string
	Success bool
}

func runShell(dir, command string) commandResult {
	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		code = 1
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		}
	}
	return commandResult{Code: code, Stdout: stdout.String(), Stderr: stderr.String(), Success: err == nil}
}

func checkoutRef(ref string) (string, error) {
	dir, err := os.MkdirTemp("", "sx-ref-*")
	if err != nil {
		return "", err
	}
	data, err := archiveRef(ref)
	if err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	if err := extractTar(dir, data); err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

func archiveRef(ref string) ([]byte, error) {
	cmd := exec.Command("git", "archive", "--format=tar", ref)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git archive %s: %w\n%s", ref, err, stderr.String())
	}
	return stdout.Bytes(), nil
}

func extractTar(dir string, data []byte) error {
	tr := tar.NewReader(bytes.NewReader(data))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if err := extractTarEntry(dir, hdr, tr); err != nil {
			return err
		}
	}
	return nil
}

func extractTarEntry(dir string, hdr *tar.Header, r io.Reader) error {
	target, err := safeArchivePath(dir, hdr.Name)
	if err != nil {
		return err
	}
	switch hdr.Typeflag {
	case tar.TypeDir:
		return os.MkdirAll(target, 0o755)
	case tar.TypeReg:
		return writeTarFile(target, os.FileMode(hdr.Mode), r)
	default:
		return nil
	}
}

func safeArchivePath(dir, name string) (string, error) {
	target := filepath.Clean(filepath.Join(dir, name))
	cleanDir := filepath.Clean(dir)
	if target != cleanDir && !strings.HasPrefix(target, cleanDir+string(os.PathSeparator)) {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	return target, nil
}

func writeTarFile(target string, mode os.FileMode, r io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func runIn(dir string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, string(out))
	}
	return nil
}
