package ga

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"purgatrix/internal/suggest"
)

func TestLoadCasesHaveCommands(t *testing.T) {
	cases, err := LoadCases(CorpusCases)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) < 3 {
		t.Fatalf("got %d cases, want at least 3", len(cases))
	}
	for _, c := range cases {
		if c.Name == "" || c.DBTCommand == "" || c.Summary == "" {
			t.Errorf("case %+v is incompletely defined", c)
		}
	}
}

func TestMaterializeBuildsBaseAndHeadCommits(t *testing.T) {
	var cases []Case
	for _, corpus := range []string{CorpusCases, CorpusHoldout, CorpusHard} {
		loaded, err := LoadCases(corpus)
		if err != nil {
			t.Fatal(err)
		}
		cases = append(cases, loaded...)
	}
	for _, c := range cases {
		dir := filepath.Join(t.TempDir(), c.Name)
		if err := Materialize(c, dir); err != nil {
			t.Fatalf("case %s: %v", c.Name, err)
		}
		log, err := gitOutput(dir, "log", "--format=%s")
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Fields(log); len(got) != 2 || got[0] != "head" || got[1] != "base" {
			t.Fatalf("case %s: git log = %q, want head then base", c.Name, log)
		}
		patch, err := ReferencePatch(c, dir)
		if err != nil {
			t.Fatalf("case %s: %v", c.Name, err)
		}
		if !strings.Contains(patch, "--- a/") {
			t.Errorf("case %s: reference patch is not a unified diff:\n%s", c.Name, patch)
		}
		// The head tree must be restored after rendering the reference patch.
		status, err := gitOutput(dir, "status", "--porcelain")
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(status) != "" {
			t.Errorf("case %s: worktree left dirty: %q", c.Name, status)
		}
		// Head must be a working repository, or the case cannot measure anything.
		cmd := exec.Command("sh", "-c", c.DBTCommand)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("case %s: dbt command failed: %v\n%s", c.Name, err, string(out))
		}
	}
}

func TestScoreCaseRewardsDeepAcceptedReductions(t *testing.T) {
	c := Case{Name: "grade", Ceiling: 400}
	shallow := scoreCase(c, suggest.Report{Accepted: []suggest.Outcome{{Accepted: true, Applied: true, TestsPassed: true, DBTMatch: true, Scored: true, Improvement: -100}}}, 1)
	deep := scoreCase(c, suggest.Report{Accepted: []suggest.Outcome{{Accepted: true, Applied: true, TestsPassed: true, DBTMatch: true, Scored: true, Improvement: -400}}}, 1)
	if !(deep.Score > shallow.Score) {
		t.Fatalf("deep reduction scored %.1f, shallow scored %.1f; deep must win", deep.Score, shallow.Score)
	}
	if deep.BestReduction != 400 || shallow.BestReduction != 100 {
		t.Errorf("reductions recorded as deep=%d shallow=%d", deep.BestReduction, shallow.BestReduction)
	}
}

func TestScoreCaseCapsAtCeiling(t *testing.T) {
	c := Case{Name: "grade", Ceiling: 400}
	at := scoreCase(c, suggest.Report{Accepted: []suggest.Outcome{{Accepted: true, Improvement: -400}}}, 1)
	beyond := scoreCase(c, suggest.Report{Accepted: []suggest.Outcome{{Accepted: true, Improvement: -900}}}, 1)
	if at.Score != beyond.Score {
		t.Fatalf("score beyond the ceiling (%.1f) must equal score at the ceiling (%.1f)", beyond.Score, at.Score)
	}
}

func TestScoreCaseCreditsBeatingTheReferenceWhenUncapped(t *testing.T) {
	c := Case{Name: "grade", Ceiling: 400}
	report := suggest.Report{Accepted: []suggest.Outcome{{Accepted: true, Improvement: -500}}}
	at := scoreCase(c, suggest.Report{Accepted: []suggest.Outcome{{Accepted: true, Improvement: -400}}}, 1.5)
	beyond := scoreCase(c, report, 1.5)
	if !(beyond.Score > at.Score) {
		t.Fatalf("with the cap raised, beating the reference (%.1f) must outscore matching it (%.1f)", beyond.Score, at.Score)
	}
}

func TestScoreCasePenalizesSloppyCandidates(t *testing.T) {
	c := Case{Name: "grade", Ceiling: 400}
	clean := suggest.Report{Accepted: []suggest.Outcome{{Accepted: true, Improvement: -200}}}
	sloppy := suggest.Report{
		Accepted: clean.Accepted,
		Rejected: []suggest.Outcome{
			{Reason: "candidate patch did not apply"},
			{Applied: true, Reason: "tests failed"},
			{Applied: true, TestsPassed: true, Reason: "observable command output differed"},
		},
	}
	if scoreCase(c, sloppy, 1).Score >= scoreCase(c, clean, 1).Score {
		t.Fatalf("sloppy run (%.1f) must score below the clean run (%.1f)", scoreCase(c, sloppy, 1).Score, scoreCase(c, clean, 1).Score)
	}
}

func TestScoreCaseZeroWithoutCandidates(t *testing.T) {
	result := scoreCase(Case{Name: "grade", Ceiling: 400}, suggest.Report{}, 1)
	if result.Score != 0 {
		t.Fatalf("empty run scored %.1f, want 0", result.Score)
	}
	if !strings.Contains(result.Diagnostic, "no candidates") {
		t.Errorf("diagnostic = %q", result.Diagnostic)
	}
}

func TestScoreCaseNeverNegative(t *testing.T) {
	result := scoreCase(Case{Name: "grade", Ceiling: 400}, suggest.Report{
		Rejected: []suggest.Outcome{{Reason: "a"}, {Reason: "b"}, {Reason: "c"}},
	}, 1)
	if result.Score < 0 {
		t.Fatalf("score %.1f must be floored at 0", result.Score)
	}
}

func TestIndividualFitnessIsRobustToOneBadRound(t *testing.T) {
	// A prompt that measures 88 three times and once collapses to 58 (a
	// provider hiccup) must still rank as an 88-class prompt.
	ind := Individual{Samples: []float64{88.3, 88.2, 58.3, 88.3}}
	if got := ind.Fitness(); got != 88.25 {
		t.Fatalf("Fitness = %.2f, want the median 88.25", got)
	}
	if got := ind.MeanFitness(); got >= 84 {
		t.Fatalf("MeanFitness = %.1f; the mean should be the one dragged down", got)
	}
	if got := (Individual{}).Fitness(); got != 0 {
		t.Fatalf("unmeasured individual Fitness = %.1f, want 0", got)
	}
	if got := (Individual{Samples: []float64{5}}).Fitness(); got != 5 {
		t.Fatalf("single-sample Fitness = %.1f, want 5", got)
	}
}

func TestSeedPopulationIsFiveDistinctPrompts(t *testing.T) {
	seeds := SeedPrompts()
	if len(seeds) != 5 {
		t.Fatalf("got %d seeds, want 5", len(seeds))
	}
	seen := map[string]bool{}
	for _, s := range seeds {
		if len(s.Prompt) < minPromptChars {
			t.Errorf("seed %s is only %d chars", s.ID, len(s.Prompt))
		}
		if seen[s.Prompt] {
			t.Errorf("seed %s duplicates another seed", s.ID)
		}
		seen[s.Prompt] = true
	}
}

func TestCleanPromptStripsFences(t *testing.T) {
	got := cleanPrompt("```\nSimplify the code.\n```")
	if got != "Simplify the code." {
		t.Fatalf("cleanPrompt = %q", got)
	}
}

func TestValidatePromptRejectsDuplicatesAndOutliers(t *testing.T) {
	long := strings.Repeat("x", maxPromptChars+1)
	if err := validatePrompt(long, nil); err == nil {
		t.Error("expected an over-long prompt to be rejected")
	}
	if err := validatePrompt("too short", nil); err == nil {
		t.Error("expected a too-short prompt to be rejected")
	}
	ok := strings.Repeat("y", minPromptChars+10)
	if err := validatePrompt(ok, []string{ok}); err == nil {
		t.Error("expected a duplicate prompt to be rejected")
	}
	if err := validatePrompt(ok, []string{"other"}); err != nil {
		t.Errorf("expected a valid prompt to pass, got %v", err)
	}
}

func TestSelectWinnerPrefersResampledConsistency(t *testing.T) {
	lucky := Individual{ID: "lucky", Samples: []float64{70}}
	steady := Individual{ID: "steady", Samples: []float64{68, 68, 68, 68, 68, 68, 68, 68}}
	best, shortlist := selectWinner([]RoundRecord{{Round: 1, Ranked: []Individual{lucky, steady}}})
	if best.ID != "steady" {
		t.Fatalf("winner = %s, want steady (mean 68 over 8) to beat a single lucky 70", best.ID)
	}
	if len(shortlist) != 2 {
		t.Fatalf("shortlist has %d entries, want 2", len(shortlist))
	}
}

func TestSelectWinnerKeepsMostResampledSnapshot(t *testing.T) {
	early := Individual{ID: "elite", Samples: []float64{50}}
	late := Individual{ID: "elite", Samples: []float64{50, 50, 50}}
	best, _ := selectWinner([]RoundRecord{
		{Round: 1, Ranked: []Individual{early}},
		{Round: 2, Ranked: []Individual{late}},
	})
	if best.Evaluations() != 3 {
		t.Fatalf("winner carries %d evaluations, want the 3-sample snapshot", best.Evaluations())
	}
}

func TestDirectivesRotate(t *testing.T) {
	if directiveFor(1, 0) == directiveFor(1, 1) {
		t.Error("both fresh individuals in a round got the same directive")
	}
}
