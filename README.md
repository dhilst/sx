# purgatrix

`purgatrix` implements a first-pass Structural Complexity optimizer pipeline.

The deterministic tooling owns:

```text
Go source -> .sx IR -> SCG scoring -> caching -> PR comparison -> DBT-backed candidate acceptance
```

External suggestion providers may generate candidate patches, but they do not
calculate or modify SC scores.

## CLI

Build or run the CLI:

```bash
go run ./cmd/sx <command>
```

Commands:

```bash
sx compile [-o out.sx] path/to/file.go
sx score [-json] path/to/file.go
sx score [-json] path/to/file.sx
sx repo [-json] [--ignore "glob,glob"] .
sx pr --base <ref> [--head <ref|WORKTREE>] [-json]
sx suggest --base <ref> [--head <ref>] [--candidate patch.diff] [--test "go test ./..."]
sx suggest --base <ref> [--head <ref>] --provider-command "<cmd>" [--prompt-file prompt.txt]
sx suggest --base <ref> --provider-command "<cmd>" --write-patches out/
```

An accepted suggestion is only useful if it can be applied. Candidates are
validated in throwaway checkouts, so the patch is carried back into the report
(`patch` in JSON) and `--write-patches` saves each accepted simplification to a
file that `git apply` takes as-is.

`sx suggest` returns only accepted simplifications. A candidate is accepted only
when:

```text
tests pass on the original head
tests pass on the candidate
DBT observes identical exit code/stdout/stderr for the configured command
candidate PR score is lower than original PR score
```

The default DBT command is:

```bash
go test ./...
```

A candidate that fails earlier than that is rejected, never fatal: one
unparseable or unappliable patch does not abandon the other candidates. Each
outcome reports how far it got, so a caller can tell the failure modes apart:

```text
applied      the patch applied to a clean checkout of head
tests_passed the configured command succeeded on both sides
dbt_match    exit code, stdout, and stderr were byte-identical
scored       the candidate translated and scored, so its SC number is real
```

`--prompt-file` replaces the built-in provider prompt. The built-in prompt lives
in `internal/suggest.DefaultPrompt` and is the seed of the prompt optimizer
below.

## Analyze

`sx analyze` reports what the graph says can be simplified, and what sx computes
each opportunity is worth, with no provider involved:

```bash
sx analyze [-json] [-n 20] [--ignore "glob"] .
```

The graph carries no expression semantics, so an opportunity is a **plan**: what
to change, where, what it is worth, and any precondition the graph could check.
Executing a plan and proving it preserves behaviour stay with the caller. Plans
whose ranges overlap are reduced to a non-conflicting set by weighted interval
selection.

Savings are computed from the same weights the scorer uses. They are close, not
exact: `guard_inversion` models the depth term precisely and ignores breadth,
which measured 67 against a scorer charge of 63 on `path/filepath/symlink.go`.
An exact figure requires applying the rewrite to the graph and rescoring.

## What Gets Scored

Repository scoring walks `.go` files, excluding `_test.go`, and skips what the
go tool itself ignores: directories beginning with `.` or `_`, plus `vendor`
and `testdata`. Fixtures and build artifacts are not the program, and charging
a repository for them makes its score meaningless.

Build constraints are honoured, so a package with per-platform files scores the
files that build for this platform. Scores are therefore platform-specific,
exactly as the compiled program is. Without this, any package carrying
`_unix.go` and `_windows.go` variants fails outright: both declare the same
functions, and the merge sees duplicate identities.

`--ignore` takes a comma-separated list of globs on top of that, accepted by
`repo`, `pr`, and `suggest`:

```bash
sx repo --ignore "internal/legacy,*_gen.go,cmd/*/mock" .
```

A pattern is matched against the repository-relative path and against the final
path element, and a pattern naming a directory excludes everything beneath it.
A malformed glob is an error rather than a silently empty filter. On `suggest`,
ignored paths are left out of both the score and the suggestions.

## Suggestion Provider Contract

`--provider-command` runs an external command. The command receives JSON on
stdin containing:

```text
base/head refs
original PR SC score
base/head repository totals
head root scores
head hotspots
original git diff
instruction not to fabricate SC scores
```

The command should print one unified diff to stdout, or JSON of the form
`{"candidates":[{"description":"...","patch":"<unified diff>"}]}`. The wire
types live in `internal/suggest`.

Declining is a first-class answer. Empty stdout and an explicit
`{"candidates":[]}` both mean "no credible simplification", and neither is an
error. Anything that parses as JSON is taken at its word; only non-JSON output
is treated as a raw diff. A provider that correctly finds nothing must not be
charged for a candidate it never proposed.

## Claude-backed Provider

`cmd/sxprovider` implements that contract against the `claude` CLI:

```bash
go build -o /tmp/sxprovider ./cmd/sxprovider
sx suggest --base <ref> --provider-command /tmp/sxprovider --test "<dbt command>"
```

It hands the model the current head text of the hotspot files and asks for
**whole-file rewrites**, then synthesises the unified diff itself with
`git diff --no-index`. Models are unreliable at hand-writing hunk headers, and a
patch that fails to apply tells you nothing about the simplification it was
trying to express.

The provider enforces in code what the prompt only asks for: it drops rewrites
of `_test.go` files and of any file it did not offer, so a candidate cannot pass
acceptance by editing its own tests.

```text
SX_PROVIDER_MODEL        model to run (default sonnet)
SX_PROVIDER_TIMEOUT_SEC  per-call timeout (default 300)
```

## Prompt Optimizer

`cmd/sxga` evolves the suggestion prompt with a genetic algorithm. Fitness is
measured, never judged: every prompt in the population generates real candidate
patches, and every candidate goes through the same acceptance pipeline as any
other suggestion.

```bash
go run ./cmd/sxga --rounds 10 --concurrency 5
```

Each generation of five prompts is ranked, then bred:

```text
rank 1, 2  survive unchanged and are re-measured
rank 2 + 3 are merged into one crossover individual
rank 4, 5  are dropped for two fresh individuals on rotating strategy directives
```

The corpus lives in `internal/ga/testdata/cases`. Each case is a small Go
repository with a base commit, a head commit whose new code carries avoidable
structure, and a reference simplification. The reference is validated through
`sx suggest` at startup, and the SC reduction it achieves becomes that case's
ceiling, so fitness is expressed as a fraction of attainable headroom rather
than a raw score.

```text
grade  nested if/else pyramids            reference removes 467
route  a six-arm else-if ladder           reference removes 556
wrap   a chain of pass-through wrappers   reference removes 156
```

`--corpus hard` selects a second corpus whose cases carry **behaviour traps**:
the obvious simplification silently changes output, and only a candidate that
checks its edge cases survives the behaviour test.

```text
validate  arms report an asymmetric number of problems   reference removes  67
summary   singular and plural wording differ             reference removes 231
engine    ten arms whose statuses differ from values     reference removes 901
```

Traps matter because a corpus without them saturates: if every prompt reaches
the reference, fitness stops discriminating. `--ratio-cap` addresses the same
problem from the other side — above 1.0, a candidate that beats the reference
simplification scores higher than one that merely matches it.

Fitness rewards acceptance and the depth of the measured reduction, and
subtracts points for candidates that fail to apply, break tests, or change
observable output, so a prompt cannot win by spraying guesses. Because the
provider is a language model, fitness is noisy: surviving individuals are
re-measured every round and ranked on their mean, and the final winner is
chosen on a mean discounted by sample size so a single lucky round cannot take
the title.

Artifacts land in `.sxga/run-<stamp>/`: every prompt, a per-round record, the
run log, and `best-prompt.txt`.

A prompt bred against three cases can simply have learned those three cases, so
`internal/ga/testdata/holdout` holds a case evolution never sees, and any prompt
can be measured against it directly:

```bash
go run ./cmd/sxga --measure .sxga/run-<stamp>/best-prompt.txt --corpus holdout
```

## Cache

Repository scoring uses a content-addressed translation cache under:

```text
.git/sx-cache/
```

When no Git directory is available, it falls back to:

```text
.sx-cache/
```

The cache key includes:

```text
source unit path
source bytes
.sx schema version
SC model version
Go frontend version
Go translation spec version
```

This allows unchanged files at the same path to be reused across branches,
worktrees, and PR analyses while preserving source mappings.
