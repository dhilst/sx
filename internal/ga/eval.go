package ga

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"purgatrix/internal/suggest"
)

// Evaluator measures prompts with the real sx acceptance pipeline.
type Evaluator struct {
	SXPath       string
	ProviderPath string
	WorkDir      string
	Cases        []Case
	Env          []string

	// RatioCap bounds reduction credit as a multiple of the reference
	// simplification. 0 means 1.0.
	RatioCap float64
}

// MeasureCeilings materialises each case, validates its reference
// simplification through sx, and records the measured SC reduction as that
// case's attainable ceiling.
func (e *Evaluator) MeasureCeilings(ctx context.Context) error {
	for i := range e.Cases {
		c := e.Cases[i]
		dir := filepath.Join(e.WorkDir, "ceiling", c.Name)
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
		if err := Materialize(c, dir); err != nil {
			return err
		}
		patch, err := ReferencePatch(c, dir)
		if err != nil {
			return err
		}
		patchPath := filepath.Join(dir, "reference.patch")
		if err := os.WriteFile(patchPath, []byte(patch), 0o644); err != nil {
			return err
		}
		report, _, err := e.runSuggest(ctx, dir, []string{"--candidate", patchPath}, c)
		if err != nil {
			return fmt.Errorf("case %s: measure reference ceiling: %w", c.Name, err)
		}
		if len(report.Accepted) == 0 {
			return fmt.Errorf("case %s: reference simplification was not accepted, so the case has no attainable ceiling", c.Name)
		}
		best := 0
		for _, o := range report.Accepted {
			if r := o.Reduction(); r > best {
				best = r
			}
		}
		e.Cases[i].Ceiling = best
	}
	return nil
}

// Evaluate measures one prompt across the whole corpus.
func (e *Evaluator) Evaluate(ctx context.Context, label, prompt string) PromptResult {
	var results []CaseResult
	for _, c := range e.Cases {
		results = append(results, e.evaluateCase(ctx, label, prompt, c))
	}
	return aggregate(results)
}

func (e *Evaluator) evaluateCase(ctx context.Context, label, prompt string, c Case) CaseResult {
	dir := filepath.Join(e.WorkDir, "eval", label, c.Name)
	if err := os.RemoveAll(dir); err != nil {
		return CaseResult{Case: c.Name, Ceiling: c.Ceiling, Error: err.Error(), Diagnostic: "harness could not prepare the case"}
	}
	if err := Materialize(c, dir); err != nil {
		return CaseResult{Case: c.Name, Ceiling: c.Ceiling, Error: err.Error(), Diagnostic: "harness could not materialise the case"}
	}
	promptPath := filepath.Join(dir, "..", c.Name+"-prompt.txt")
	if err := os.WriteFile(promptPath, []byte(prompt), 0o644); err != nil {
		return CaseResult{Case: c.Name, Ceiling: c.Ceiling, Error: err.Error(), Diagnostic: "harness could not write the prompt"}
	}
	report, stderr, err := e.runSuggest(ctx, dir, []string{
		"--provider-command", e.ProviderPath,
		"--prompt-file", promptPath,
	}, c)
	if err != nil {
		return CaseResult{
			Case:       c.Name,
			Ceiling:    c.Ceiling,
			Error:      firstLine(err.Error()),
			Diagnostic: "sx suggest failed: " + firstLine(stderr),
		}
	}
	return scoreCase(c, report, e.RatioCap)
}

func (e *Evaluator) runSuggest(ctx context.Context, dir string, extra []string, c Case) (suggest.Report, string, error) {
	args := append([]string{
		"suggest",
		"--base", "HEAD~1",
		"--head", "HEAD",
		"--test", c.DBTCommand,
		"-json",
	}, extra...)
	cmd := exec.CommandContext(ctx, e.SXPath, args...)
	cmd.Dir = dir
	if len(e.Env) > 0 {
		cmd.Env = append(os.Environ(), e.Env...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return suggest.Report{}, stderr.String(), err
	}
	var report suggest.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		return suggest.Report{}, stderr.String(), fmt.Errorf("decode sx suggest output: %w", err)
	}
	return report, stderr.String(), nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	if len(s) > 300 {
		return s[:300]
	}
	return s
}
