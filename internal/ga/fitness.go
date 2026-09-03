package ga

import (
	"fmt"
	"sort"
	"strings"

	"purgatrix/internal/suggest"
)

// Fitness weights. Acceptance dominates, the depth of the measured SC
// reduction is the main gradient, and sloppy candidates cost points so that a
// prompt cannot win by spraying guesses.
const (
	acceptanceBonus      = 20.0
	reductionWeight      = 60.0
	extraAcceptedBonus   = 5.0
	maxExtraAccepted     = 2
	validButUselessBonus = 2.0
	penaltyNotApplied    = 4.0
	penaltyTestsFailed   = 3.0
	penaltyDBTMismatch   = 2.0
)

// CaseResult is one prompt measured against one evaluation case.
type CaseResult struct {
	Case          string  `json:"case"`
	Score         float64 `json:"score"`
	Candidates    int     `json:"candidates"`
	AcceptedCount int     `json:"accepted_count"`
	BestReduction int     `json:"best_reduction"`
	Ceiling       int     `json:"ceiling"`
	Diagnostic    string  `json:"diagnostic"`
	Error         string  `json:"error,omitempty"`
}

// PromptResult is one prompt measured across the whole corpus.
type PromptResult struct {
	Fitness float64      `json:"fitness"`
	Cases   []CaseResult `json:"cases"`
}

// scoreCase converts an sx suggestion report into fitness. Every input is a
// measurement produced by sx; nothing here re-derives an SC number.
//
// ratioCap bounds the credit for reducing SC, expressed as a multiple of the
// reference simplification. At 1.0 a prompt that beats the reference scores the
// same as one that matches it, which hides differences when a corpus is easy
// enough that every prompt reaches the reference.
func scoreCase(c Case, report suggest.Report, ratioCap float64) CaseResult {
	if ratioCap <= 0 {
		ratioCap = 1
	}
	result := CaseResult{Case: c.Name, Ceiling: c.Ceiling}
	outcomes := append(append([]suggest.Outcome{}, report.Accepted...), report.Rejected...)
	result.Candidates = len(outcomes)
	result.AcceptedCount = len(report.Accepted)

	if len(outcomes) == 0 {
		result.Diagnostic = "emitted no candidates at all"
		return result
	}

	score := 0.0
	var failures []string
	for _, o := range outcomes {
		switch {
		case o.Accepted:
			// Counted below, via the best reduction.
		case !o.Applied:
			score -= penaltyNotApplied
			failures = append(failures, "a candidate did not apply: "+o.Reason)
		case !o.TestsPassed:
			score -= penaltyTestsFailed
			failures = append(failures, "a candidate broke the tests")
		case !o.DBTMatch:
			score -= penaltyDBTMismatch
			failures = append(failures, "a candidate changed observable output")
		default:
			score += validButUselessBonus
			failures = append(failures, "a candidate was safe but did not lower measured SC")
		}
	}

	for _, o := range report.Accepted {
		if r := o.Reduction(); r > result.BestReduction {
			result.BestReduction = r
		}
	}
	if result.AcceptedCount > 0 {
		score += acceptanceBonus
		if c.Ceiling > 0 {
			ratio := float64(result.BestReduction) / float64(c.Ceiling)
			if ratio > ratioCap {
				ratio = ratioCap
			}
			if ratio < 0 {
				ratio = 0
			}
			score += reductionWeight * ratio
		}
		extra := result.AcceptedCount - 1
		if extra > maxExtraAccepted {
			extra = maxExtraAccepted
		}
		score += extraAcceptedBonus * float64(extra)
	}
	if score < 0 {
		score = 0
	}
	result.Score = score
	result.Diagnostic = diagnose(result, failures)
	return result
}

func diagnose(result CaseResult, failures []string) string {
	var parts []string
	if result.AcceptedCount > 0 {
		pct := 0
		if result.Ceiling > 0 {
			pct = result.BestReduction * 100 / result.Ceiling
		}
		parts = append(parts, fmt.Sprintf("%d/%d candidates accepted, best measured SC reduction %d (%d%% of the reference simplification)",
			result.AcceptedCount, result.Candidates, result.BestReduction, pct))
	} else {
		parts = append(parts, fmt.Sprintf("0/%d candidates accepted", result.Candidates))
	}
	parts = append(parts, dedupe(failures)...)
	return strings.Join(parts, "; ")
}

func dedupe(items []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range items {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func aggregate(cases []CaseResult) PromptResult {
	result := PromptResult{Cases: cases}
	if len(cases) == 0 {
		return result
	}
	total := 0.0
	for _, c := range cases {
		total += c.Score
	}
	result.Fitness = total / float64(len(cases))
	return result
}

// Diagnostics renders a prompt's per-case feedback for the meta-agent that
// breeds the next generation.
func (r PromptResult) Diagnostics() string {
	var b strings.Builder
	for _, c := range r.Cases {
		fmt.Fprintf(&b, "- case %s (score %.1f): %s", c.Case, c.Score, c.Diagnostic)
		if c.Error != "" {
			fmt.Fprintf(&b, " [harness error: %s]", c.Error)
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}
