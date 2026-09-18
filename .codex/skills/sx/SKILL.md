---
name: "sx"
description: "Use when the user asks Codex to minimize a Go codebase with sx, run /sx:min, bake /sx:bake eg examples, or add AST-reducing eg templates to a Go project."
---

# sx

Use this skill when the user asks to minimize a Go codebase with `sx`, asks for
`/sx:min`, asks for `/sx:bake`, or asks to add `eg` examples for AST-reducing
rewrites.

`sx` applies negative pressure to Go code size. It measures AST nodes, proposes
shrinking refactors, applies them behind build/test/measure gates, and keeps only
changes that reduce AST size.

## /sx:min [all|auto]

Minimize the current Go repository in a temporary git worktree, then review the
result with the user.

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

6. Run minimization inside the worktree:

   ```bash
   go tool sx refactor -apply -n 100 "$tmp/worktree"
   ```

   If using an installed binary:

   ```bash
   sx refactor -apply -n 100 "$tmp/worktree"
   ```

7. Run the project test command in the worktree. Prefer the repository's
   documented test command. Use `go test ./...` when no stronger local command
   exists.
8. Review `git -C "$tmp/worktree" diff`.
9. In `all` mode, copy the accepted diff back to the original repository after
   tests pass.
10. In `auto` mode, apply only the changes you judge acceptable. Explain any
    rejected changes briefly.
11. Remove the temporary worktree:

    ```bash
    git worktree remove "$tmp/worktree"
    ```

Never add a co-author trailer unless the user explicitly asks for one.

## /sx:bake [path]

Create `eg` examples from recurring expressions in the current codebase. These
rules are ordinary Go files that `sx refactor -eg <path>` can try later.

Default path:

```text
sx/examples/eg
```

Procedure:

1. Use the provided path, or `sx/examples/eg` if no path is provided.
2. Inspect the Go codebase for repeated expression forms where a larger
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
5. Run `go tool sx refactor -check -eg <path> .` or the local equivalent to
   confirm the examples are valid and can be evaluated.
6. Keep only templates that parse, type-check under `eg`, and reduce AST size
   when they match.

The default minimization command searches `examples/eg` and `sx/examples/eg`, so
examples baked to the default path are picked up by later `/sx:min` runs.

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
