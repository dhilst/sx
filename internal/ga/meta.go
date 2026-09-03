package ga

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const (
	minPromptChars = 200
	maxPromptChars = 4000
	metaAttempts   = 3
)

// briefing tells the breeding agent exactly how a prompt is used and how it is
// scored, so it optimizes against the real fitness function rather than a
// guess about it.
const briefing = `You are optimizing the system prompt that sx sends to its code-simplification provider.

HOW THE PROMPT IS USED
The provider prepends your prompt to a package containing: sx's measured scores, the ranked
hotspot list, the full current text of the hotspot files, and the PR diff. It then appends a
fixed OUTPUT CONTRACT that requires exactly one JSON object of whole-file rewrites:
{"candidates":[{"description":"...","files":[{"path":"...","content":"<complete new file>"}]}]}
The contract always wins, so the prompt must never specify a different output format, never ask
for unified diffs, and never demand commentary or explanation outside the JSON.

HOW A PROMPT IS SCORED
Each candidate is pushed through sx's deterministic acceptance pipeline on real Go repositories:
1. the synthesised patch must apply,
2. the configured test command must pass,
3. exit code, stdout, and stderr must be byte-identical to the unpatched head (behaviour test),
4. the candidate's measured Structural Complexity must be lower than the original head's.
Fitness rewards acceptance, and above all the depth of the measured SC reduction relative to a
known-good reference simplification. Extra accepted candidates add a little. Candidates that fail
to apply, break tests, or change observable output subtract points, so precision matters more
than volume. Emitting nothing scores zero.

WHAT sx's SC MODEL ACTUALLY CHARGES FOR
- every structural node costs its kind weight multiplied by (1 + nesting depth), so deep nesting
  is quadratically expensive: branch=2, multi_branch=3, loop=3, call=2, defer=3, closure=2
- Go else-if chains nest, so a long else-if ladder is far more expensive than a flat switch
- control/call edges cost extra, and unresolved or external calls cost more still
- every parameter, result, local, captured, and global costs state weight by state depth, and
  excess arity beyond 3 is penalised
- structural and state cycles cost 4 each
So: flatten nesting into guard clauses, collapse else-if ladders into flat switches, hoist
duplicated guards, delete pass-through wrapper functions, and drop redundant intermediate locals.
Formatting-only changes and new abstractions do not lower the score.

HARD RULES FOR THE PROMPT YOU WRITE
- Address the provider agent in the imperative. Under 300 words.
- Never instruct it to compute, estimate, or edit SC scores; sx owns every number.
- Never instruct it to modify _test.go files.
- Output ONLY the prompt text: no preamble, no commentary, no markdown fences, no headings that
  announce your process.`

// Breeder generates the next generation with a headless claude call.
type Breeder struct {
	Model   string
	Timeout time.Duration
}

// Crossover merges two parents into one prompt, guided by what the harness
// measured about each.
func (b *Breeder) Crossover(ctx context.Context, a, c Individual, taken []string) (string, error) {
	instruction := fmt.Sprintf(`%s

PARENT A scored %.1f fitness.
--- PARENT A PROMPT ---
%s
--- MEASURED BEHAVIOUR OF PARENT A ---
%s

PARENT B scored %.1f fitness.
--- PARENT B PROMPT ---
%s
--- MEASURED BEHAVIOUR OF PARENT B ---
%s

TASK
Write ONE merged prompt that keeps the instructions the measurements credit for accepted, deep SC
reductions and repairs the failures the measurements expose. It must not be a copy of either
parent. Output only the merged prompt text.`,
		briefing, a.Fitness(), a.Prompt, a.Result.Diagnostics(), c.Fitness(), c.Prompt, c.Result.Diagnostics())
	return b.generate(ctx, instruction, taken)
}

// Fresh writes a new individual that explores a distinct strategy while
// staying inside the harness contract.
func (b *Breeder) Fresh(ctx context.Context, best Individual, directive string, taken []string) (string, error) {
	instruction := fmt.Sprintf(`%s

The best prompt so far scored %.1f fitness.
--- BEST PROMPT SO FAR ---
%s
--- MEASURED BEHAVIOUR OF THE BEST PROMPT ---
%s

TASK
Write a genuinely NEW prompt, not a variation of the one above, that pursues this strategy:
%s
It must still satisfy every hard rule. Output only the new prompt text.`,
		briefing, best.Fitness(), best.Prompt, best.Result.Diagnostics(), directive)
	return b.generate(ctx, instruction, taken)
}

func (b *Breeder) generate(ctx context.Context, instruction string, taken []string) (string, error) {
	var lastErr error
	for attempt := 1; attempt <= metaAttempts; attempt++ {
		message := instruction
		if attempt > 1 {
			message += fmt.Sprintf("\n\nRETRY %d: the previous reply was rejected (%v). Reply with the prompt text only, between %d and %d characters, and make it distinct from every prompt shown above.",
				attempt, lastErr, minPromptChars, maxPromptChars)
		}
		reply, err := b.call(ctx, message)
		if err != nil {
			lastErr = err
			continue
		}
		prompt := cleanPrompt(reply)
		if err := validatePrompt(prompt, taken); err != nil {
			lastErr = err
			continue
		}
		return prompt, nil
	}
	return "", fmt.Errorf("breeder produced no usable prompt after %d attempts: %w", metaAttempts, lastErr)
}

func (b *Breeder) call(ctx context.Context, message string) (string, error) {
	timeout := b.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(callCtx, "claude", "-p", "--model", b.Model, "--allowedTools", "", "--output-format", "text")
	cmd.Stdin = strings.NewReader(message)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("claude -p: %w: %s", err, firstLine(stderr.String()))
	}
	return stdout.String(), nil
}

// cleanPrompt strips markdown fences and any lead-in line the agent adds
// despite being told not to.
func cleanPrompt(reply string) string {
	text := strings.TrimSpace(reply)
	if i := strings.Index(text, "```"); i >= 0 {
		rest := text[i+3:]
		if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
			rest = rest[nl+1:]
		}
		if end := strings.Index(rest, "```"); end >= 0 {
			rest = rest[:end]
		}
		text = strings.TrimSpace(rest)
	}
	return text
}

func validatePrompt(prompt string, taken []string) error {
	if n := len(prompt); n < minPromptChars || n > maxPromptChars {
		return fmt.Errorf("prompt length %d outside [%d,%d]", n, minPromptChars, maxPromptChars)
	}
	for _, t := range taken {
		if strings.TrimSpace(t) == strings.TrimSpace(prompt) {
			return fmt.Errorf("prompt duplicates an existing population member")
		}
	}
	return nil
}

// directives rotate the exploration strategy handed to freshly created
// individuals so the population does not collapse onto one framing.
var directives = []string{
	"Name the exact Go constructs sx's cost model punishes and give concrete rewrite recipes for each, with the guard-clause and flat-switch shapes spelled out.",
	"Be terse and surgical: very few rules, and a hard push toward several small independent candidates that each attack one hotspot.",
	"Lead with behaviour-preservation discipline: enumerate what must stay byte-identical (printed output, exported signatures, error strings, evaluation order of guards) before any rewrite is proposed.",
	"Demand maximum measured reduction per candidate: attack the single highest-contribution hotspot and rewrite that whole function outright rather than nibbling at it.",
	"Impose a short internal checklist the agent must work through (locate deepest nesting, find duplicated guards, find pass-through wrappers, find redundant locals) before writing any file content.",
	"Frame the work as restoring the simplest program that produces identical observable behaviour, and warn explicitly against the failure modes the diagnostics keep showing.",
}

func directiveFor(round, slot int) string {
	return directives[(round*2+slot)%len(directives)]
}
