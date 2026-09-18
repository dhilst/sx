# sx

`sx` helps you shrink a Go module safely enough to use as a code-review aid.

It measures Go code by counting AST nodes, finds changes that should lower that
count, tries one change at a time, and keeps only changes that still build, pass
tests, and make the measured program smaller.

Use `sx` as a patch generator, not as an automatic cleanup tool. It optimizes
for small code, so you should review every diff and keep only the changes that
also make the code easier to maintain.

## Who This Is For

Use `sx` when you want to:

- find the largest functions in a Go package or module
- remove unreachable functions detected by `deadcode`
- inline one-use helpers through `gopls`
- factor repeated code when doing so reduces AST size
- apply small, example-based expression rewrites with `eg`
- fail CI when a shrinking candidate is available

Do **not** treat `sx` as proof that a change is behavior-preserving. The build,
test, and measurement gates catch many bad rewrites, but public APIs,
timing-sensitive code, exact error values, reflection, and side effects still
need human review.

## Install

In the Go module you want to shrink, add `sx` as a tool dependency:

```bash
go get -tool github.com/dhilst/sx/cmd/sx
```

Then run it with:

```bash
go tool sx .
```

Install the helper tools for refactoring candidates:

```bash
go install golang.org/x/tools/cmd/deadcode@latest
go install golang.org/x/tools/gopls@latest
go install golang.org/x/tools/cmd/eg@latest
```

You do not need every helper installed, but each missing helper disables one
class of candidate. If no usable helper is available, `sx refactor` exits with
an install message. `eg` counts as usable only when at least one template exists.

Inside this repository, `go tool sx` and `go run ./cmd/sx` build the same local
program.

## Helper Tools

`sx` decides which changes are worth trying, but it delegates the actual Go-aware
work to maintained Go tools:

| Tool | Link | Used for |
|---|---|---|
| `deadcode` | [golang.org/x/tools/cmd/deadcode](https://pkg.go.dev/golang.org/x/tools/cmd/deadcode) | Finding unreachable functions that can be deleted |
| `gopls` | [golang.org/x/tools/gopls](https://pkg.go.dev/golang.org/x/tools/gopls) | Inlining calls, extracting duplicated statement runs, and repairing imports |
| `eg` | [golang.org/x/tools/cmd/eg](https://pkg.go.dev/golang.org/x/tools/cmd/eg) | Applying example-based expression rewrites from template files |

The tools are optional in the sense that `sx` can run with only the helpers you
have installed. Missing helpers simply remove candidate classes:

- without `deadcode`, unreachable functions are not proposed
- without `gopls`, inline and deduplication candidates are not proposed
- without `eg`, example rewrite templates are not applied

At least one candidate source must be available. For `eg`, that means both the
`eg` binary and at least one template under `examples/eg`, `sx/examples/eg`, or
a path passed with `-eg`.

## Quick Start

Start with a clean git working tree so rejected or unwanted patches are easy to
inspect and undo.

### 1. Measure the current module

```bash
go tool sx .
```

This prints the total node count and the largest functions.

### 2. Preview one candidate without editing files

```bash
go tool sx refactor .
```

Without `-apply`, `sx refactor` only reports the best candidate it would try.

### 3. Apply a bounded pass

```bash
go tool sx refactor -apply -n 30 .
```

For each attempted change, `sx` formats and repairs imports, rebuilds, runs the
relevant tests, re-counts nodes, and reverts the change unless the final count is
smaller.

### 4. Review before committing

```bash
go test ./...
git diff
```

Keep the patch only if it is smaller **and** clearer for future maintainers.

## Typical Workflows

### Local minimization pass

```bash
git status --short
go tool sx refactor -apply -n 30 .
go test ./...
git diff
```

If the diff is too aggressive, discard it and run fewer attempts:

```bash
git restore .
go tool sx refactor -apply -n 5 .
```

### CI check

Use `-check` to fail when `sx` can see at least one likely shrinking candidate.
It never writes files.

```bash
go tool sx refactor -check -n 30 .
```

A `-check` candidate is only a prediction. The same candidate may be rejected by
`-apply` after the real build, test, and measurement gates run.

### Disable tests for a fast exploratory run

```bash
go tool sx refactor -apply -test=false -n 30 .
```

Use this only for exploration. Run the full test suite before keeping the patch.

## What sx Changes

`sx` currently looks for four kinds of reduction.

| Kind | Helper | What it tries |
|---|---|---|
| Dead code | `deadcode` | Remove unreachable plain functions |
| Inlining | `gopls` | Inline small functions used once |
| Deduplication | `gopls` | Extract repeated statement runs when the extraction is smaller |
| `eg` examples | `eg` | Rewrite expressions using example templates |

### Dead Code

`sx` asks `deadcode` which plain functions are unreachable from the current
program. It then tries deleting one candidate at a time and keeps the deletion
only if the build, tests, and measured AST count all pass.

### Inlining

`sx` finds small functions that appear to be used once, then asks `gopls` to run
the actual inline refactor. `gopls` owns the type-aware edit; `sx` owns the
decision about whether the resulting patch is smaller and still valid.

### Deduplication

`sx` looks for repeated statement runs in a package. When extracting the repeated
run into a helper function should reduce AST size, `sx` asks `gopls` to perform
the extraction and then replaces the other copies with calls when that remains
buildable and smaller.

This is deliberately conservative. It does not try to invent arbitrary
abstractions; it only attempts repeated code that can be represented as a normal
Go extraction and accepted by the same build, test, and measurement gates.

### `eg` Rewrites

`sx` loads `eg` templates from configured directories, prices the AST difference
between each template's `before` and `after` expressions, asks `eg` where the
template matches, and applies the rewrite only when it is selected as a candidate.

Every attempted change follows this loop:

1. choose the highest predicted saving not already tried
2. apply the candidate
3. format and repair imports
4. rebuild the package
5. run the packages that could be affected
6. count AST nodes again
7. keep the change only if the count went down

The predicted saving only decides what to try first. The final measured count is
what decides whether the change stays.

## Code Reduction Examples

These are examples of the small expression rewrites included in `examples/eg`.

```go
// before
if strings.Index(name, "/") >= 0 {
	return true
}

// after
if strings.Contains(name, "/") {
	return true
}
```

```go
// before
return fmt.Sprintf("%s", value)

// after
return value
```

```go
// before
return time.Now().Sub(start)

// after
return time.Since(start)
```

```go
// before
return bytes.Compare(a, b) == 0

// after
return bytes.Equal(a, b)
```

```go
// before
return enabled == true

// after
return enabled
```

Checked-in templates currently cover:

```text
fmt.Errorf("%s", s)       -> errors.New(s)
fmt.Sprintf("%s", s)      -> s
time.Now().Sub(t)         -> time.Since(t)
s[:len(s)]                -> s
x == true                 -> x
x != false                -> x
!!x                       -> x
bytes.Compare(a, b) == 0  -> bytes.Equal(a, b)
strings.Index(s, sub)>=0  -> strings.Contains(s, sub)
strings.Index(s, sub)==-1 -> !strings.Contains(s, sub)
```

## Add Your Own `eg` Rewrites

[`eg`](https://pkg.go.dev/golang.org/x/tools/cmd/eg) is the Go example-based
refactoring tool from `golang.org/x/tools`. An `eg` template is a Go file with a
`before` function and an `after` function. Both functions must have the same
type.

### Minimal template

```go
//go:build ignore

package template

func before(s string) string { return s[:len(s)] }
func after(s string) string  { return s }
```

To copy this into your own module:

```bash
mkdir -p sx/examples/eg
$EDITOR sx/examples/eg/full-string-slice.go
go tool sx refactor -check -eg sx/examples/eg .
go tool sx refactor -apply -eg sx/examples/eg .
go test ./...
git diff
```

You can also pass any template directory to `sx`:

```bash
go tool sx refactor -check -eg ./examples/eg .
go tool sx refactor -apply -eg ./examples/eg .
```

The `-eg` flag can be repeated:

```bash
go tool sx refactor -apply -eg ./examples/eg -eg ./team/eg .
```

It can also take comma-separated paths:

```bash
go tool sx refactor -apply -eg ./examples/eg,./team/eg .
```

Disable `eg` rewrites by passing an empty `-eg` value:

```bash
go tool sx refactor -apply -eg "" .
```

By default, `sx` searches these directories if they exist:

```text
examples/eg
sx/examples/eg
```

### Template-writing checklist

- Keep `before` and `after` the same type.
- Prefer one returned expression in each function.
- Add imports normally when the expressions need them.
- Use `//go:build ignore` so templates are not compiled into your module.
- Avoid rules that duplicate, remove, or reorder expressions with side effects.
- Start with narrow, obvious rewrites and let `sx` prove the measured saving.

## Command Reference

Measure Go files:

```bash
go tool sx [-json] [-n 20] [-tests] <paths...>
```

Refactor a module:

```bash
go tool sx refactor [-apply] [-check] [-n 10] [-test=false] [-eg path] <dir>
```

Useful flags:

| Flag | Command | Meaning |
|---|---|---|
| `-json` | measure | Emit measurement output as JSON |
| `-n` | measure | Number of functions to list; `0` lists all |
| `-tests` | measure | Include `_test.go` files when measuring |
| `-apply` | refactor | Write accepted changes; without it, preview one candidate |
| `-check` | refactor | Exit non-zero when a shrinking candidate is found; never writes files |
| `-n` | refactor | Number of candidates to attempt; default is `10` |
| `-test=false` | refactor | Skip tests after each accepted-looking change |
| `-eg` | refactor | File or directory of `eg` templates; repeatable |

## CI Example

```yaml
name: sx

on:
  pull_request:
  push:
    branches: [main]

jobs:
  minimize:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - run: go install golang.org/x/tools/cmd/deadcode@latest
      - run: go install golang.org/x/tools/gopls@latest
      - run: go install golang.org/x/tools/cmd/eg@latest
      - run: go tool sx refactor -check -n 30 .
```

## Troubleshooting

| Symptom | What to do |
|---|---|
| `sx refactor` says helper tools are missing | Install at least one of `deadcode`, `gopls`, or `eg` |
| `eg` templates are ignored | Confirm `eg` is installed and templates are in a searched path or passed with `-eg` |
| A candidate appears in `-check` but is not kept by `-apply` | This is expected when the real gates reject the change or the measured count does not improve |
| The diff is too broad | Restore the tree and rerun with a smaller `-n` |
| A rewrite looks smaller but less readable | Reject it; `sx` optimizes for AST size, not taste |

## Adversarial User Review

A skeptical user should challenge `sx` before trusting its output:

- **"Will this silently rewrite my whole repository?"** No. Preview mode is the
  default. `sx` writes files only with `-apply`, and rejected changes are
  reverted.
- **"Can it prove the rewrite is correct?"** No. It builds, tests, and measures;
  it does not prove semantic equivalence.
- **"Why should I trust the predicted savings?"** You should not trust them as
  final results. They only rank candidates. Kept patches must produce a smaller
  measured AST after the rewrite.
- **"What if smaller code is worse code?"** Then reject the diff. The tool is
  useful when it finds simplifications, not when it wins a code-golf contest.
- **"What should I review most carefully?"** Public APIs, error text and error
  wrapping, reflection, concurrency, timing, generated code boundaries, and
  expressions with side effects.

## Assistant Support

This repository includes assistant-facing instructions for running `sx`
repeatably and safely:

```text
.codex/skills/sx/SKILL.md
.claude/commands/sx/min.md
.claude/commands/sx/bake.md
```

[`.codex/skills/sx/SKILL.md`](.codex/skills/sx/SKILL.md) is a Codex skill with
metadata, trigger guidance, and procedures for:

- minimizing a Go repository with `sx`
- baking new `eg` templates from repeated expression patterns
- adding hand-written `eg` rules to a project

[`.claude/commands/sx/min.md`](.claude/commands/sx/min.md) documents
`/sx:min [all|auto]`. It runs minimization in a temporary git worktree and
reviews the resulting patch before applying accepted changes.

[`.claude/commands/sx/bake.md`](.claude/commands/sx/bake.md) documents
`/sx:bake [path]`. It asks the assistant to create new `eg` examples from
expression patterns in the codebase. If `path` is omitted, it writes to
`sx/examples/eg`, which `sx` searches by default.

The assistant workflows intentionally use a temporary git worktree for
minimization. That keeps the user's checkout clean while `sx` tries patches,
then gives the assistant a diff to review and copy back only when it is worth
keeping.

## License

Apache License 2.0. See [LICENSE](LICENSE).
