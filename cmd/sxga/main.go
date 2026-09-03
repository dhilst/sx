// Command sxga optimizes the sx suggestion prompt with a genetic algorithm.
//
// Fitness is measured, not judged: every prompt in the population generates
// real candidate patches, and every candidate is pushed through sx's
// deterministic acceptance pipeline (apply, tests, behaviour test, measured SC
// reduction) against a corpus of Go repositories with known headroom.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"purgatrix/internal/ga"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "sxga:", err)
		os.Exit(1)
	}
}

func run() error {
	rounds := flag.Int("rounds", 10, "number of generations")
	outDir := flag.String("out", "", "directory for prompts, per-round records, and the winner (default .sxga/run-<stamp>)")
	sxPath := flag.String("sx", "", "path to the sx binary (default: build it from this module)")
	providerPath := flag.String("provider", "", "path to the suggestion provider (default: build cmd/sxprovider from this module)")
	providerModel := flag.String("provider-model", "sonnet", "model the suggestion provider runs on")
	metaModel := flag.String("meta-model", "sonnet", "model that performs crossover and mutation")
	concurrency := flag.Int("concurrency", 5, "prompts measured in parallel")
	evalTimeout := flag.Duration("eval-timeout", 30*time.Minute, "timeout for measuring one prompt across the corpus")
	corpus := flag.String("corpus", "cases", "evaluation corpus: cases, hard, or holdout")
	ratioCap := flag.Float64("ratio-cap", 1.0, "credit ceiling for SC reduction, as a multiple of the reference simplification; above 1.0 a candidate that beats the reference scores higher")
	measure := flag.String("measure", "", "measure one prompt file against the corpus and exit, instead of evolving")
	flag.Parse()

	corpusPath := ga.CorpusCases
	switch *corpus {
	case "cases":
	case "hard":
		corpusPath = ga.CorpusHard
	case "holdout":
		corpusPath = ga.CorpusHoldout
	default:
		return fmt.Errorf("unknown corpus %q: want cases, hard, or holdout", *corpus)
	}

	root, err := moduleRoot()
	if err != nil {
		return err
	}
	out := *outDir
	if out == "" {
		out = filepath.Join(root, ".sxga", "run-"+time.Now().Format("20060102-150405"))
	}
	// Every path handed to the evaluator must be absolute: it runs sx with the
	// working directory set to the evaluation repository, where a relative path
	// resolves against the wrong root.
	if out, err = filepath.Abs(out); err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	binDir := filepath.Join(out, "bin")
	if *sxPath == "" {
		if *sxPath, err = build(root, binDir, "sx"); err != nil {
			return err
		}
	}
	if *providerPath == "" {
		if *providerPath, err = build(root, binDir, "sxprovider"); err != nil {
			return err
		}
	}
	if *sxPath, err = filepath.Abs(*sxPath); err != nil {
		return err
	}
	if *providerPath, err = filepath.Abs(*providerPath); err != nil {
		return err
	}

	logPath := filepath.Join(out, "run.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return err
	}
	defer logFile.Close()
	log := &teeWriter{a: os.Stdout, b: logFile}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := ga.Config{
		Rounds:        *rounds,
		Corpus:        corpusPath,
		OutDir:        out,
		SXPath:        *sxPath,
		ProviderPath:  *providerPath,
		ProviderModel: *providerModel,
		MetaModel:     *metaModel,
		Concurrency:   *concurrency,
		EvalTimeout:   *evalTimeout,
		RatioCap:      *ratioCap,
		Log:           log,
	}

	if *measure != "" {
		return measurePrompt(ctx, cfg, log, *measure, *corpus)
	}

	fmt.Fprintf(log, "sxga: %d rounds, corpus=%s, provider=%s meta=%s, artifacts in %s\n",
		*rounds, *corpus, *providerModel, *metaModel, out)
	result, err := ga.Run(ctx, cfg)
	if err != nil {
		return err
	}

	fmt.Fprintln(log, "\n=== fitness by round ===")
	for _, r := range result.Rounds {
		fmt.Fprintf(log, "round %2d  best %-28s %.1f\n", r.Round, r.BestID, r.BestFitness)
	}
	fmt.Fprintln(log, "\n=== final shortlist (median fitness, discounted by sample size) ===")
	for i, ind := range result.Shortlist {
		fmt.Fprintf(log, "%d. %-28s origin=%-9s median=%.1f mean=%.1f over %d evaluations\n",
			i+1, ind.ID, ind.Origin, ind.Fitness(), ind.MeanFitness(), ind.Evaluations())
	}
	fmt.Fprintf(log, "\nincumbent prompt, first round: %.1f\n", result.IncumbentF)
	fmt.Fprintf(log, "winner: %s (origin %s) median fitness %.1f over %d evaluations\n",
		result.Best.ID, result.Best.Origin, result.Best.Fitness(), result.Best.Evaluations())
	fmt.Fprintf(log, "winning prompt written to %s\n", filepath.Join(out, "best-prompt.txt"))
	return nil
}

// measurePrompt scores one prompt file against a corpus, which is how a
// winning prompt gets checked against held-out cases it was never bred on.
func measurePrompt(ctx context.Context, cfg ga.Config, log io.Writer, promptPath, corpus string) error {
	prompt, err := os.ReadFile(promptPath)
	if err != nil {
		return err
	}
	label := "measure-" + filepath.Base(promptPath)
	fmt.Fprintf(log, "measuring %s against corpus %s\n", promptPath, corpus)
	result, cases, err := ga.Measure(ctx, cfg, label, string(prompt))
	if err != nil {
		return err
	}
	for _, c := range cases {
		fmt.Fprintf(log, "case %-6s reference ceiling %d\n", c.Name, c.Ceiling)
	}
	fmt.Fprintf(log, "\nfitness %.1f\n", result.Fitness)
	fmt.Fprintln(log, result.Diagnostics())
	return nil
}

func build(root, binDir, name string) (string, error) {
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(binDir, name)
	cmd := exec.Command("go", "build", "-o", path, "./cmd/"+name)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build ./cmd/%s: %w\n%s", name, err, string(out))
	}
	return path, nil
}

func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod found above the working directory")
		}
		dir = parent
	}
}

type teeWriter struct {
	a, b *os.File
}

func (t *teeWriter) Write(p []byte) (int, error) {
	n, err := t.a.Write(p)
	if err != nil {
		return n, err
	}
	if _, err := t.b.Write(p); err != nil {
		return n, err
	}
	return n, nil
}
