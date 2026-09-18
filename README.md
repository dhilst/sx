# sx

Negative pressure on the size of a Go codebase.

Large language models generate code quickly, and most development loops make it
easier to add code than to remove it. `sx` pushes in the opposite direction: it
measures Go code by AST size, finds transformations that may reduce that size,
applies them, and keeps only changes that make the program smaller while still
building and passing tests.

The objective is deliberately narrow. `sx` optimizes for fewer AST nodes, not
for readability. It may prefer longer functions, fewer helper functions, and
inlining that a human reviewer would reject. Treat its output as a patch to
review, not as a formatter.

## Quick Start

Add `sx` to a Go module as a tool dependency:

```bash
go get -tool github.com/dhilst/sx/cmd/sx
```

That records it in `go.mod` and gives you `go tool sx` in that module: no binary
on `PATH`, and the version pinned next to every other dependency.

Install the external tools used for transformations:

```bash
go install golang.org/x/tools/cmd/deadcode@latest
go install golang.org/x/tools/gopls@latest
go install golang.org/x/tools/cmd/eg@latest
```

Measure a module:

```bash
go tool sx .
```

Preview one shrinking candidate:

```bash
go tool sx refactor .
```

Apply candidates and keep only changes that pass the gates:

```bash
go tool sx refactor -apply -n 30 .
```

Run the tests after the pass:

```bash
go test ./...
```

Inspect the patch before committing:

```bash
git diff
```

Inside this repository the tool dependency is its own `cmd/sx`, so `go tool sx`
and `go run ./cmd/sx` build the same program. The examples below use the tool
form because it is the one that also works from a module that only depends on
`sx`.

## The measure

```text
|AST| — the number of AST nodes it takes to express the program
```

Counting nodes rather than lines makes the measure independent of formatting and
of comments, and it has no parameters: there is nothing to tune and nothing to
validate against anyone's judgement. It is also additive — a node costs one
wherever it sits — which is what makes it usable as a target rather than only as
a ranking.

Command form:

```bash
go tool sx [-json] [-n 20] [-tests] <paths...>
```

## The transformations

The objective is `min |AST|` subject to behaviour: the same outputs and side
effects for the same inputs and state. Four things remove nodes.

| primitive | what it exploits | measured on this repository |
|---|---|---|
| **dead code** | a declaration nothing can reach | −41 in one move, the largest single win |
| **duplication** | the same code written more than once | −39 in one move |
| **inlining** | a function that only forwards | 17 moves, −2 to −27 each |
| **example rewrites** | a larger expression with an equivalent smaller one | whatever the checked-in `eg` templates match |

Detection is this tool's job. The transformations are not, wherever a maintained
tool already does one properly:

- **`deadcode`** answers reachability across a whole program, which a file at a
  time syntactic pass cannot.
- **`gopls`** inlines a call and extracts a function using type information —
  free variables, return values, and whether the change is legal at all.
- **`eg`** applies example-based expression rewrites from `examples/eg`.

```bash
go install golang.org/x/tools/cmd/deadcode@latest
go install golang.org/x/tools/gopls@latest
go install golang.org/x/tools/cmd/eg@latest

go tool sx refactor [-apply] [-check] [-n 30] [-eg examples/eg] <dir>
```

None of these tools has any notion of which change is worth making. That is what the
measure supplies.

### Example rewrites

`eg` templates are Go files with a `before` function and an `after` function.
Both functions must have the same type. `sx` asks `eg` where a template matches,
estimates the AST saving, applies the template only when selected, and then runs
the same build, test and measurement gates used for every other change.

Default template search paths:

```text
examples/eg
sx/examples/eg
```

Pass your own templates with `-eg`. The flag may be repeated, and each value may
contain comma-separated paths or paths separated by the operating system path-list
separator.

```bash
go tool sx refactor -apply -eg ./my-eg-rules -eg ./team/rules/time.go .
go tool sx refactor -apply -eg ./my-eg-rules,./team/rules .
```

Disable example rewrites:

```bash
go tool sx refactor -apply -eg "" .
```

The checked-in examples cover small expression reductions such as:

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

### CI mode

Use `-check` to fail a build when a shrinking candidate exists. Check mode
detects candidates and stops at the first one: it prints what it would do and
exits non-zero. It never writes to the tree.

That is deliberately weaker than what `-apply` knows. `-apply` earns its numbers
by making the change and measuring it, and a check that edits the code it is
checking is the wrong shape for CI. So `-check` reports the prediction rather
than the proven saving, and the prediction is routinely wrong in both
directions - including candidates that turn out not to be applicable at all.
Expect it to fail on work `-apply` would end up rejecting.

```bash
go tool sx refactor -check -n 30 .
```

GitHub Actions example:

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

`go tool sx` resolves from the `tool` directive in your `go.mod`, so the version
CI runs is the one the repository pins. If you would rather not record the
dependency, `go run github.com/dhilst/sx/cmd/sx@latest refactor -check -n 30 .`
works too, at the cost of an unpinned version.

### Assistant commands

This repository includes project instructions for Codex and Claude:

```text
.codex/skills/sx/SKILL.md
.claude/commands/sx/min.md
.claude/commands/sx/bake.md
```

`/sx:min [all|auto]` runs minimization in a temporary git worktree and then
reviews the resulting patch.

- `all` accepts every change that passes `sx` gates.
- `auto` lets the model reject changes that reduce AST size but make the code
  harder to maintain.

`/sx:bake [path]` asks the model to create new `eg` examples from expression
patterns in the codebase. If `path` is omitted, it writes to `sx/examples/eg`.
That path is searched by default by later minimization runs.

## Nothing is kept on trust

After every change the tree is re-counted, rebuilt and re-tested. A change that
does not shrink the program, or that breaks the build, or that fails the tests,
is put back.

The prediction attached to each candidate only orders the attempts. It is
routinely wrong in both directions - it said -27 for something worth -8, and
-14 for something worth -39 - so the measurement afterwards is what decides.

A failed build is read before it is judged. Removing the last user of a package
leaves its import behind, and Go will not compile that: the transformation was
unfinished, not unsafe. Unused imports are tidied and the build retried. Nothing
else is repaired, because a fix that needs to guess what the author meant is
another edit, with its own measurement and its own gate.

## What a change can break is not what it touches

The tests that run are the changed package's **and every package that imports
it**, which `go list` works out once per run.

Testing only what was edited is not a smaller version of this - it is a
different thing that looks the same. Refactoring `internal/runtime/maps` passed
that package's own suite, which is good enough to reject six other inlines in
the same run, and left `reflect` corrupting type descriptors:

```text
fatal error: runtime: name offset base pointer out of range
```

The whole standard library still built, and every per-package gate was green.
It took running `crypto/aes`'s tests to see it. The honest gate costs what it
costs: for that package it is 261 packages and four and a half minutes per
attempted change.

## Where it will not go

Three gates - build, tests, measure - agree on a great deal that is wrong,
because each has a boundary and the loop finds whatever lives outside it. So
the boundaries are declared rather than discovered:

| left alone | because |
|---|---|
| files this build does not compile | `go build` and `go test` cannot say whether the edit is safe. Editing them rewrote Windows, plan9, wasip1 and s390x source with every gate green |
| generated files | the edit is erased on the next run, and the file says so |
| test files | the measure does not count them, so moving a body there reads as a saving. On a two-function package: 29 nodes to 12, nothing deleted |
| bodies using `unsafe` | what `unsafe.Pointer` guarantees depends on where a value lives and how long. `reflectlite.packEface` carries a comment that its correctness depends on no operation coming between two assignments |
| functions under a `//go:` directive | `//go:nosplit` fixes a stack budget; moving a body in is what it forbids |
| names declared once per platform | which one a call resolves to is a build-tag question |
| functions held as values | `var Exported = unexported` in an export_test.go is a live reference that is not a call |

Every one of those was found by running this on the Go standard library, and
every one of them built, passed its tests, and measured smaller.

## Why greedy

Greedy is wrong when a move only pays by enabling another, and inlining is
exactly that case: on its own it always makes the program bigger, because the
body then exists at the call site *and* in the declaration. It only becomes a
saving once the declaration goes. So the two are applied as one move, and
`deadcode` confirms the abstraction really can go.

With that pair made atomic, searching over orderings stops paying. A beam search
was built and measured against the greedy loop on this codebase:

```text
greedy    -318 nodes (-4.0%), 22 changes, converged      2m11s
beam      -174 nodes (-2.2%),  8 changes, depth-limited  6m01s
```

It lost for structural reasons rather than tuning ones. A beam of depth d can
never make more than d changes, while greedy runs until the candidates are gone.
Width bought nothing: at depth 8 the best six states spanned 8 nodes and were
permutations of the same moves, because these transformations commute. The one
thing it found that greedy cannot see was an ordering worth 13 nodes.

So the search was removed. It is worth rebuilding the day a transformation
appears whose order actually matters.

## What it does

On its own source, to a fixed point:

```text
8369 -> 8289 nodes   (-80, -1.0%)
8 changes kept, 21 attempted
```

Running it again keeps nothing: a second pass re-offers everything the first
rejected, in the same order, with the same verdicts. It is a fixed point of
these three primitives and no more.

On 55 packages of the Go standard library, gated on every importer, with the
result left building and passing `go test std`:

```text
nodes  294086 -> 292157   (-1929, -0.7%)
raw     77674 ->  76250   (-1424, -1.8%)
code    53605 ->  53343   ( -262, -0.5%)
156 changes kept
```

Read those three lines together. **Of the 1424 lines removed, 1162 are comments
and blank lines** - removing a function deletes its documentation, while the
body does not go anywhere, it moves to the call site. Half a percent of the code
went. "1424 lines saved" would be mostly a report of deleted documentation.

That is 55 of the 117 leaf packages with candidates. Twelve more were already
failing or too slow to gate. The remaining fifty were left out because gating
them honestly is unaffordable - `internal/cpu` has 267 importers,
`internal/runtime/maps` 260 - and those are exactly the packages with the most
to remove. The excluded half is the expensive half, not a random sample.

For comparison, from this repository's own history:

```text
deleting the beam search        -1564 nodes of 8033    (-19.5%)
the standard library sweep      -1929 nodes of 294086   (-0.7%)
```

Deciding one feature did not earn its place beat the whole automated pipeline
applied to the standard library. Nothing here can find that: it measures what
exists, and cannot ask whether it should.

## What it costs

`min |AST|` says a function called once is always a loss: you pay for the
declaration, the signature, the return and the call, and get one use back. So it
deletes single-use abstractions, and the fixed point has none left. Seventeen
helpers went that way in the run above.

That is the objective working, not failing, but it is the whole objective. It
knows nothing about whether the result is easier to read.

## License

Apache License 2.0. See [LICENSE](LICENSE).
