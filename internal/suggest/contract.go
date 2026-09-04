// Package suggest defines the wire contract between sx and external
// suggestion providers. Providers receive a Context on stdin and emit a
// ProviderResponse (or a raw unified diff) on stdout. Providers never
// calculate SC scores; sx owns every number in this contract.
package suggest

import (
	"purgatrix/internal/analyze"
	"purgatrix/internal/sc"
)

type Context struct {
	BaseRef         string          `json:"base_ref"`
	HeadRef         string          `json:"head_ref"`
	OriginalPRScore int             `json:"original_pr_score"`
	BaseTotal       int             `json:"base_total"`
	HeadTotal       int             `json:"head_total"`
	HeadRoots       []sc.RootReport `json:"head_roots"`
	Hotspots        []sc.Hotspot    `json:"hotspots"`

	// Plans are the deterministic opportunities sx found in the graph, each
	// with the saving measured by rewriting the graph and rescoring. A
	// provider does not have to guess what is worth doing.
	Plans []analyze.Plan `json:"plans,omitempty"`

	HotspotSources     []SourceSnippet `json:"hotspot_sources"`
	OriginalDiff       string          `json:"original_diff"`
	Prompt             string          `json:"prompt"`
	Transformations    []string        `json:"transformations"`
	AcceptanceCriteria []string        `json:"acceptance_criteria"`
	OutputFormat       string          `json:"output_format"`
	Instruction        string          `json:"instruction"`
}

type SourceSnippet struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Text      string `json:"text"`
}

type CandidatePatch struct {
	Description string `json:"description"`
	Patch       string `json:"patch"`
}

type ProviderResponse struct {
	Candidates  []CandidatePatch `json:"candidates"`
	Description string           `json:"description"`
	Patch       string           `json:"patch"`
}

// Report is the result of a suggestion run: the original PR score plus every
// candidate sx measured, split by whether it cleared acceptance.
type Report struct {
	OriginalPRScore int       `json:"original_pr_score"`
	Accepted        []Outcome `json:"accepted"`
	Rejected        []Outcome `json:"rejected,omitempty"`
}

// Outcome records how far one candidate got through the acceptance pipeline.
// The stage flags let callers tell "did not apply" apart from "applied but did
// not lower measured SC".
type Outcome struct {
	Description string `json:"description"`
	Accepted    bool   `json:"accepted"`
	Reason      string `json:"reason,omitempty"`

	// Patch carries the accepted simplification itself. Without it the report
	// states that a simplification exists and how much SC it removes, but
	// leaves no way to apply it.
	Patch string `json:"patch,omitempty"`

	Applied          bool     `json:"applied"`
	TestsPassed      bool     `json:"tests_passed"`
	DBTMatch         bool     `json:"dbt_match"`
	Scored           bool     `json:"scored"`
	OriginalPRScore  int      `json:"original_pr_score"`
	CandidatePRScore int      `json:"candidate_pr_score"`
	Improvement      int      `json:"improvement"`
	DBTResult        string   `json:"dbt_result"`
	TestsExecuted    []string `json:"tests_executed"`
}

// Add files an outcome under accepted or rejected.
func (r *Report) Add(o Outcome) {
	if o.Accepted {
		r.Accepted = append(r.Accepted, o)
		return
	}
	r.Rejected = append(r.Rejected, o)
}

// Reduction is the measured SC drop a candidate delivered, positive when the
// candidate is simpler than the original head.
func (o Outcome) Reduction() int {
	return -o.Improvement
}
