---
name: "sx"
description: "Use when the user asks Codex to minimize a Go codebase with sx, run $sx min or /sx min, bake $sx bake or /sx bake eg examples, or add AST-reducing eg templates to a Go project."
---

# sx

Use this skill when the user asks to minimize a Go codebase with `sx`, asks for
`$sx min`, `/sx min`, `$sx bake`, `/sx bake`, or asks to add `eg` examples for
AST-reducing rewrites.

`sx` applies negative pressure to Go code size. It measures AST nodes, proposes
shrinking refactors, applies them behind build/test/measure gates, and keeps only
changes that reduce AST size.

## Command syntax

Use one command shape across Codex-style skill invocation and slash-command
clients:

```text
{$|/}sx <cmd> [params]
```

Commands:

- `min [auto|all] [path]`: minimize the current Go repository, or only the code
  under `path`. Default mode is `auto`; default path is the repository root.
- `bake [dest] [src]`: create `eg` examples from the code under `src` and write
  them to `dest`. Default `dest` is `sx/examples/eg`; default `src` is the
  repository root.

A single parameter after `min` is the mode if it is `auto` or `all`, and the
path otherwise. A single parameter after `bake` is `dest`.

Examples:

```text
$sx min auto
$sx min all
$sx min ./internal/storage
$sx min auto ./internal/storage
$sx bake
$sx bake ./examples/eg
$sx bake ./examples/eg ./internal
/sx min auto ./internal/storage
/sx bake ./examples/eg ./internal
```

Legacy spellings such as `/sx:min` and `/sx:bake` mean the same thing when a
client exposes them.

## sx min [auto|all] [path]

Minimize the current Go repository, or only the code under `path`, in a
temporary git worktree, then review the result with the user. `path` is relative
to the repository root. Only code under it is changed, and the tests `sx` runs
after each change cover every package under `path` and every package in the
module that imports one of them.

Modes:

- `all`: accept every `sx` change that passes the tool gates.
- `auto`: inspect the resulting diff and accept only changes that are reasonable
  for the codebase. Reject changes that make the code materially harder to
  maintain, even if they reduce AST size.

Default mode is `auto`.

Procedure:

1. Find the target repository root with `git rev-parse --show-toplevel`.
2. Check `git status --short`. If the tree is dirty, stop and ask the user
   whether to commit the changes first or abort. Do not continue without an
   answer, and do not fall back to minimizing `HEAD` while uncommitted changes
   sit beside it: the worktree is created from `HEAD`, so the diff would be
   against code the user does not have and may not apply to their tree.
   Untracked files count - a package that was never added is invisible to the
   run.
3. Create a temporary worktree from `HEAD`:

   ```bash
   tmp=$(mktemp -d)
   git worktree add -d "$tmp/worktree" HEAD
   ```

4. Locate the `sx` command. Prefer these in order:

   ```bash
   go tool sx
   go run ./cmd/sx
   sx
   ```

   `go tool sx` works when the target module declares `sx` as a tool dependency;
   add it with `go get -tool github.com/dhilst/sx/cmd/sx` if the user agrees to
   the go.mod change. `go run ./cmd/sx` only works inside the `sx` source
   repository. Otherwise use an installed `sx` binary, or ask the user for the
   path.

5. Install missing helper tools if network access and policy allow it:

   ```bash
   go install golang.org/x/tools/cmd/deadcode@latest
   go install golang.org/x/tools/gopls@latest
   go install golang.org/x/tools/cmd/eg@latest
   ```

6. Run minimization inside the worktree, on `path` (`.` when none was given):

   ```bash
   go tool sx refactor -apply -n 100 "$tmp/worktree/$path"
   ```

   If using an installed binary:

   ```bash
   sx refactor -apply -n 100 "$tmp/worktree/$path"
   ```

   When the project's tests are slow, add `-batch`: each change is committed
   in the worktree, the tests run once at the end, a failure is bisected to
   the change that caused it, and the run is squashed into one commit. Review
   it with `git -C "$tmp/worktree" show` instead of `diff`; the separate
   commits are under `refs/sx/runs/`. `sx status` lists past runs.

   If the build needs files that are not committed (frontend assets under
   `//go:embed`, generated code), create them in the worktree first, or every
   change is reverted as "stopped building". `sx` prints the compiler's first
   error when it reverts one.

7. Run the project test command in the worktree. Prefer the repository's
   documented test command. Use `go test ./...` when no stronger local command
   exists.
8. Review `git -C "$tmp/worktree" diff`.
9. In `all` mode, copy the accepted diff back to the original repository after
   tests pass.
10. In `auto` mode, apply only the changes you judge acceptable. Explain any
    rejected changes briefly. Before applying, give each function `gopls`
    extracted (`newFunction`, `newFunction1`, ...) a name that says what it
    does, with `gopls rename -w <file>:<line>:<col> <name>`. Renaming changes
    no node count and makes most extractions worth keeping.
11. Remove the temporary worktree:

    ```bash
    git worktree remove "$tmp/worktree"
    ```

Never add a co-author trailer unless the user explicitly asks for one.

## sx bake [dest] [src]

Create `eg` examples from recurring expressions in the current codebase, or in
the code under `src`. These rules are ordinary Go files that
`sx refactor -eg <dest>` can try later.

Defaults: `dest` is `sx/examples/eg`, `src` is the repository root.

Procedure:

1. Use the provided `dest` and `src`, or their defaults.
2. Inspect the Go code under `src` for repeated expression forms where a larger
   expression can be replaced by a smaller equivalent expression.
3. Write each candidate as an `eg` template:

   ```go
   //go:build ignore

   package template

   func before(...) T { return largerExpression }
   func after(...) T  { return smallerExpression }
   ```

4. Prefer examples that do not duplicate, remove, or reorder wildcard
   expressions with side effects.
5. Run `go tool sx refactor -check -eg <dest> <src>` or the local equivalent to
   confirm the examples are valid and can be evaluated.
6. Keep only templates that parse, type-check under `eg`, and reduce AST size
   when they match.

The default minimization command searches `examples/eg` and `sx/examples/eg`,
both under the path being minimized, its module root, and the repository root, so examples baked
to the default `dest` are picked up by later `sx min` runs.

## Adding eg rules by hand

Use this when the user asks how to add custom examples to their own codebase.

1. Create a directory in the target repository, usually `sx/examples/eg` or
   `examples/eg`.
2. Add one `.go` file per rule. Keep the file excluded from normal builds:

   ```go
   //go:build ignore

   package template

   func before(s string) string { return s[:len(s)] }
   func after(s string) string  { return s }
   ```

3. Keep `before` and `after` the same type, and prefer single-expression
   functions. `sx` prices the difference between those expressions before asking
   `eg` to apply the rule.
4. Avoid rules that delete, duplicate, or reorder arguments that could have side
   effects.
5. Validate the rules from the repository root:

   ```bash
   go tool sx refactor -check -eg sx/examples/eg .
   ```

   Inside the `sx` source checkout, use:

   ```bash
   go run ./cmd/sx refactor -check -eg sx/examples/eg .
   ```

6. Apply them only behind the normal gates:

   ```bash
   go tool sx refactor -apply -eg sx/examples/eg .
   ```
