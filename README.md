# sx

`sx` helps you make a Go codebase smaller.

It counts Go AST nodes, looks for changes that should reduce that count, applies
one change at a time, and keeps only the changes that still build, pass tests,
and actually make the program smaller.

Use it as a patch generator, not as a formatter. `sx` optimizes for size, so you
should still review the diff and reject changes that hurt clarity.

## Install

In the Go module you want to shrink, add `sx` as a tool dependency:

```bash
go get -tool github.com/dhilst/sx/cmd/sx
```

That pins the version in `go.mod` and lets you run:

```bash
go tool sx .
```

Install the helper tools used for refactoring:

```bash
go install golang.org/x/tools/cmd/deadcode@latest
go install golang.org/x/tools/gopls@latest
go install golang.org/x/tools/cmd/eg@latest
```

Inside this repository, `go tool sx` and `go run ./cmd/sx` build the same local
program.

## Quick Start

Measure the current module:

```bash
go tool sx .
```

Preview the best candidate without editing files:

```bash
go tool sx refactor .
```

Apply up to 30 candidates, keeping only changes that pass the gates:

```bash
go tool sx refactor -apply -n 30 .
```

Review the result:

```bash
go test ./...
git diff
```

Use `-check` in CI to fail when `sx` can see at least one likely shrinking
candidate:

```bash
go tool sx refactor -check -n 30 .
```

## What sx Changes

`sx` looks for four kinds of reduction.

| Kind | Example |
|---|---|
| Dead code | remove functions or declarations that nothing reaches |
| Inlining | replace a tiny one-use helper with its body |
| Duplication | extract repeated code when doing so reduces AST size |
| `eg` examples | rewrite one expression shape into a smaller equivalent shape |

Every attempted change goes through the same loop:

1. apply the candidate
2. run formatting and import repair
3. rebuild
4. run the relevant tests
5. count AST nodes again
6. keep the change only if the node count went down

The predicted saving is only used to choose what to try first. The final count is
what decides whether the patch stays.

## Code Reduction Examples

These are the kind of expression reductions included in `examples/eg`.

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

The checked-in examples currently cover:

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

## Using eg Examples

`eg` is the Go example-based refactoring tool from `golang.org/x/tools`. An
`eg` template is a Go file with a `before` function and an `after` function.
Both functions must have the same type.

Example template:

```go
//go:build ignore

package template

import "strings"

func before(s, sub string) bool { return strings.Index(s, sub) >= 0 }
func after(s, sub string) bool  { return strings.Contains(s, sub) }
```

Save templates in a directory and pass that directory to `sx`:

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

By default, `sx` searches these directories:

```text
examples/eg
sx/examples/eg
```

## Tutorial: Add eg Examples to Your Codebase

This is a small end-to-end example you can copy into your own Go module.

1. Create a rule directory:

   ```bash
   mkdir -p sx/examples/eg
   ```

2. Add one file per rewrite. For example, create
   `sx/examples/eg/full-string-slice.go`:

   ```go
   //go:build ignore

   package template

   func before(s string) string { return s[:len(s)] }
   func after(s string) string  { return s }
   ```

3. Check whether the rule finds a shrinking candidate:

   ```bash
   go tool sx refactor -check -eg sx/examples/eg .
   ```

4. Apply it behind the normal build, test, and measurement gates:

   ```bash
   go tool sx refactor -apply -eg sx/examples/eg .
   ```

5. Review the patch:

   ```bash
   go test ./...
   git diff
   ```

When writing your own rules:

- keep `before` and `after` the same type
- prefer a single returned expression in each function
- add imports normally when the expressions need them
- use `//go:build ignore` so templates are not compiled into your module
- avoid rules that duplicate, remove, or reorder expressions with side effects
- start with narrow, obvious rewrites and let `sx` prove the measured saving

## Command Reference

Measure Go files:

```bash
go tool sx [-json] [-n 20] [-tests] <paths...>
```

Refactor a module:

```bash
go tool sx refactor [-apply] [-check] [-n 30] [-test=false] [-eg path] <dir>
```

Useful flags:

| Flag | Meaning |
|---|---|
| `-apply` | write changes; without it, only preview one candidate |
| `-check` | fail when a shrinking candidate is found; never writes files |
| `-n` | number of candidates to attempt |
| `-test=false` | skip tests after each accepted-looking change |
| `-eg` | file or directory of `eg` templates |
| `-json` | emit measurement output as JSON |
| `-tests` | include `_test.go` files when measuring |

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

## Assistant Support

This repository includes instructions for coding assistants:

```text
.codex/skills/sx/SKILL.md
.claude/commands/sx/min.md
.claude/commands/sx/bake.md
```

`/sx:min [all|auto]` runs minimization in a temporary git worktree and reviews
the resulting patch.

`/sx:bake [path]` asks the assistant to create new `eg` examples from expression
patterns in the codebase. If `path` is omitted, it writes to `sx/examples/eg`,
which `sx` searches by default.

## Safety Notes

`sx` tries to preserve behavior by building, testing, and measuring after every
change. It is still not a proof of semantic equivalence. Review the diff before
committing, especially around public APIs, timing-sensitive code, error values,
and code with side effects.

`-check` is intentionally conservative for CI. It reports a predicted candidate
without editing the tree. A candidate reported by `-check` may later be rejected
by `-apply` after the real build, test, and measurement gates run.

## License

Apache License 2.0. See [LICENSE](LICENSE).
