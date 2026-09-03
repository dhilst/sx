package ga

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Individual is one prompt in the population.
type Individual struct {
	ID     string       `json:"id"`
	Origin string       `json:"origin"`
	Prompt string       `json:"prompt"`
	Result PromptResult `json:"result"`

	// Samples holds every fitness this prompt has been measured at. Fitness is
	// noisy because the provider is a language model, so a surviving individual
	// is re-measured each round.
	Samples []float64 `json:"samples"`
}

// Fitness ranks on the median rather than the mean. A single catastrophic
// round — a provider hiccup, one patch that would not apply — should not evict
// a prompt that is otherwise the strongest in the population, and with a
// handful of samples the mean is not robust to that.
func (i Individual) Fitness() float64 {
	return median(i.Samples)
}

func (i Individual) Evaluations() int { return len(i.Samples) }

func (i Individual) LastFitness() float64 { return i.Result.Fitness }

func (i Individual) MeanFitness() float64 {
	if len(i.Samples) == 0 {
		return 0
	}
	total := 0.0
	for _, s := range i.Samples {
		total += s
	}
	return total / float64(len(i.Samples))
}

func median(samples []float64) float64 {
	if len(samples) == 0 {
		return 0
	}
	sorted := append([]float64(nil), samples...)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

// Config parameterises a GA run.
type Config struct {
	Rounds        int
	Corpus        string
	OutDir        string
	SXPath        string
	ProviderPath  string
	ProviderModel string
	MetaModel     string
	Concurrency   int
	EvalTimeout   time.Duration
	RatioCap      float64
	Log           io.Writer
}

// RoundRecord is the persisted state of one generation.
type RoundRecord struct {
	Round       int          `json:"round"`
	Ranked      []Individual `json:"ranked"`
	BestID      string       `json:"best_id"`
	BestFitness float64      `json:"best_fitness"`
}

// RunResult is the outcome of the whole optimization.
type RunResult struct {
	Cases      []Case        `json:"cases"`
	Rounds     []RoundRecord `json:"rounds"`
	Best       Individual    `json:"best"`
	Shortlist  []Individual  `json:"shortlist"`
	IncumbentF float64       `json:"incumbent_first_round_fitness"`
}

// confidencePenalty discounts a mean fitness by how few samples back it, so a
// prompt that got lucky once cannot beat one that held up across rounds.
const confidencePenalty = 5.0

func adjustedFitness(i Individual) float64 {
	if i.Evaluations() == 0 {
		return 0
	}
	return i.Fitness() - confidencePenalty/math.Sqrt(float64(i.Evaluations()))
}

// selectWinner picks the final prompt across every round, keeping the latest
// (most resampled) snapshot of each individual and ranking on the pessimistic
// bound rather than the raw mean.
func selectWinner(rounds []RoundRecord) (Individual, []Individual) {
	latest := map[string]Individual{}
	for _, r := range rounds {
		for _, ind := range r.Ranked {
			if prev, ok := latest[ind.ID]; !ok || ind.Evaluations() >= prev.Evaluations() {
				latest[ind.ID] = ind
			}
		}
	}
	shortlist := make([]Individual, 0, len(latest))
	for _, ind := range latest {
		shortlist = append(shortlist, ind)
	}
	sort.SliceStable(shortlist, func(a, b int) bool {
		fa, fb := adjustedFitness(shortlist[a]), adjustedFitness(shortlist[b])
		if fa != fb {
			return fa > fb
		}
		return shortlist[a].Evaluations() > shortlist[b].Evaluations()
	})
	if len(shortlist) == 0 {
		return Individual{}, nil
	}
	if len(shortlist) > 5 {
		shortlist = shortlist[:5]
	}
	return shortlist[0], shortlist
}

// Measure evaluates a single prompt against a corpus without evolving
// anything. It is how a candidate winner is checked on held-out cases.
func Measure(ctx context.Context, cfg Config, label, prompt string) (PromptResult, []Case, error) {
	if cfg.Corpus == "" {
		cfg.Corpus = CorpusCases
	}
	cases, err := LoadCases(cfg.Corpus)
	if err != nil {
		return PromptResult{}, nil, err
	}
	evaluator := &Evaluator{
		SXPath:       cfg.SXPath,
		ProviderPath: cfg.ProviderPath,
		WorkDir:      filepath.Join(cfg.OutDir, "work"),
		Cases:        cases,
		Env:          []string{"SX_PROVIDER_MODEL=" + cfg.ProviderModel},
		RatioCap:     cfg.RatioCap,
	}
	if err := evaluator.MeasureCeilings(ctx); err != nil {
		return PromptResult{}, nil, err
	}
	return evaluator.Evaluate(ctx, label, prompt), evaluator.Cases, nil
}

// Run executes the genetic algorithm: measure the population, keep the best
// two, merge ranks 2 and 3, and replace ranks 4 and 5 with fresh explorers.
func Run(ctx context.Context, cfg Config) (RunResult, error) {
	if cfg.Rounds <= 0 {
		cfg.Rounds = 10
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 5
	}
	if cfg.EvalTimeout <= 0 {
		cfg.EvalTimeout = 30 * time.Minute
	}
	if cfg.Log == nil {
		cfg.Log = io.Discard
	}
	if cfg.Corpus == "" {
		cfg.Corpus = CorpusCases
	}
	cases, err := LoadCases(cfg.Corpus)
	if err != nil {
		return RunResult{}, err
	}
	work := filepath.Join(cfg.OutDir, "work")
	evaluator := &Evaluator{
		SXPath:       cfg.SXPath,
		ProviderPath: cfg.ProviderPath,
		WorkDir:      work,
		Cases:        cases,
		Env:          []string{"SX_PROVIDER_MODEL=" + cfg.ProviderModel},
		RatioCap:     cfg.RatioCap,
	}
	logf(cfg, "measuring reference ceilings for %d cases", len(cases))
	if err := evaluator.MeasureCeilings(ctx); err != nil {
		return RunResult{}, err
	}
	for _, c := range evaluator.Cases {
		logf(cfg, "case %-6s original PR SC headroom: reference simplification removes %d", c.Name, c.Ceiling)
	}

	breeder := &Breeder{Model: cfg.MetaModel, Timeout: 5 * time.Minute}
	population := SeedPrompts()
	result := RunResult{Cases: evaluator.Cases}
	for _, ind := range population {
		if err := persistPrompt(cfg, 1, ind); err != nil {
			return result, err
		}
	}

	for round := 1; round <= cfg.Rounds; round++ {
		logf(cfg, "=== round %d/%d: measuring %d prompts across %d cases ===", round, cfg.Rounds, len(population), len(cases))
		start := time.Now()
		population = measurePopulation(ctx, cfg, evaluator, population, round)
		sort.SliceStable(population, func(a, b int) bool {
			if population[a].Fitness() != population[b].Fitness() {
				return population[a].Fitness() > population[b].Fitness()
			}
			return population[a].LastFitness() > population[b].LastFitness()
		})
		record := RoundRecord{Round: round, Ranked: population, BestID: population[0].ID, BestFitness: population[0].Fitness()}
		result.Rounds = append(result.Rounds, record)
		if round == 1 {
			for _, ind := range population {
				if ind.ID == "incumbent" {
					result.IncumbentF = ind.LastFitness()
				}
			}
		}
		for rank, ind := range population {
			logf(cfg, "  #%d %-28s origin=%-9s fitness=%.1f (median of %d, mean %.1f) last=%.1f",
				rank+1, ind.ID, ind.Origin, ind.Fitness(), ind.Evaluations(), ind.MeanFitness(), ind.LastFitness())
			for _, c := range ind.Result.Cases {
				logf(cfg, "        %-6s %.1f  %s", c.Case, c.Score, c.Diagnostic)
			}
		}
		logf(cfg, "  round %d took %s", round, time.Since(start).Round(time.Second))
		if err := persistRound(cfg, record); err != nil {
			return result, err
		}
		if allFailed(population) {
			return result, fmt.Errorf("round %d: every prompt failed with harness errors; aborting", round)
		}
		if round == cfg.Rounds {
			break
		}
		population = breed(ctx, cfg, breeder, population, round)
	}

	result.Best, result.Shortlist = selectWinner(result.Rounds)
	if err := persistResult(cfg, result); err != nil {
		return result, err
	}
	return result, nil
}

func measurePopulation(ctx context.Context, cfg Config, evaluator *Evaluator, population []Individual, round int) []Individual {
	measured := make([]Individual, len(population))
	sem := make(chan struct{}, cfg.Concurrency)
	var wg sync.WaitGroup
	for i, ind := range population {
		wg.Add(1)
		go func(i int, ind Individual) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			evalCtx, cancel := context.WithTimeout(ctx, cfg.EvalTimeout)
			defer cancel()
			label := fmt.Sprintf("round%02d-%s", round, ind.ID)
			ind.Result = evaluator.Evaluate(evalCtx, label, ind.Prompt)
			ind.Samples = append(append([]float64(nil), ind.Samples...), ind.Result.Fitness)
			measured[i] = ind
		}(i, ind)
	}
	wg.Wait()
	return measured
}

// breed produces the next generation: ranks 1 and 2 survive untouched, ranks 2
// and 3 are merged into a new individual, and ranks 4 and 5 are dropped in
// favour of fresh explorers seeded from the leader.
func breed(ctx context.Context, cfg Config, breeder *Breeder, ranked []Individual, round int) []Individual {
	taken := make([]string, 0, len(ranked))
	for _, ind := range ranked {
		taken = append(taken, ind.Prompt)
	}
	next := []Individual{ranked[0], ranked[1]}

	type job struct {
		origin string
		id     string
		run    func() (string, error)
		backup Individual
	}
	jobs := []job{
		{
			origin: "crossover",
			id:     fmt.Sprintf("x%02d-%s+%s", round+1, short(ranked[1].ID), short(ranked[2].ID)),
			run:    func() (string, error) { return breeder.Crossover(ctx, ranked[1], ranked[2], taken) },
			backup: ranked[2],
		},
		{
			origin: "fresh",
			id:     fmt.Sprintf("f%02da", round+1),
			run:    func() (string, error) { return breeder.Fresh(ctx, ranked[0], directiveFor(round, 0), taken) },
			backup: ranked[3],
		},
		{
			origin: "fresh",
			id:     fmt.Sprintf("f%02db", round+1),
			run:    func() (string, error) { return breeder.Fresh(ctx, ranked[0], directiveFor(round, 1), taken) },
			backup: ranked[len(ranked)-1],
		},
	}
	results := make([]Individual, len(jobs))
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i, j := range jobs {
		wg.Add(1)
		go func(i int, j job) {
			defer wg.Done()
			prompt, err := j.run()
			if err != nil {
				logf(cfg, "  breeding %s failed (%v); carrying %s forward instead", j.id, err, j.backup.ID)
				results[i] = j.backup
				return
			}
			mu.Lock()
			taken = append(taken, prompt)
			mu.Unlock()
			results[i] = Individual{ID: j.id, Origin: j.origin, Prompt: prompt}
		}(i, j)
	}
	wg.Wait()
	next = append(next, results...)
	for _, ind := range next {
		if err := persistPrompt(cfg, round+1, ind); err != nil {
			logf(cfg, "  warning: could not persist prompt %s: %v", ind.ID, err)
		}
	}
	logf(cfg, "  next generation: keep %s, keep %s, merge %s+%s, plus 2 fresh",
		ranked[0].ID, ranked[1].ID, ranked[1].ID, ranked[2].ID)
	return next
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func allFailed(population []Individual) bool {
	for _, ind := range population {
		for _, c := range ind.Result.Cases {
			if c.Error == "" {
				return false
			}
		}
	}
	return true
}

func persistPrompt(cfg Config, generation int, ind Individual) error {
	dir := filepath.Join(cfg.OutDir, "prompts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	name := fmt.Sprintf("gen%02d-%s-%s.txt", generation, ind.Origin, ind.ID)
	return os.WriteFile(filepath.Join(dir, name), []byte(ind.Prompt), 0o644)
}

func persistRound(cfg Config, record RoundRecord) error {
	dir := filepath.Join(cfg.OutDir, "rounds")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return writeJSONFile(filepath.Join(dir, fmt.Sprintf("round%02d.json", record.Round)), record)
}

func persistResult(cfg Config, result RunResult) error {
	if err := os.MkdirAll(cfg.OutDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(cfg.OutDir, "best-prompt.txt"), []byte(result.Best.Prompt), 0o644); err != nil {
		return err
	}
	return writeJSONFile(filepath.Join(cfg.OutDir, "result.json"), result)
}

func writeJSONFile(path string, v any) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func logf(cfg Config, format string, args ...any) {
	fmt.Fprintf(cfg.Log, format+"\n", args...)
}
