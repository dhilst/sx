# /sx bake [dest] [src]

Create `eg` templates for `sx` from expression patterns in the current Go
codebase, or only in the code under `src`.

Canonical invocation:

```text
/sx bake
/sx bake ./examples/eg
/sx bake ./examples/eg ./internal
```

Legacy `/sx:bake [dest] [src]` means the same thing when supported by the client.

Parameters:

- `dest`: where the templates are written. Default `sx/examples/eg`.
- `src`: the code searched for patterns, relative to the repository root.
  Default: the repository root.

Steps:

1. Use the provided `dest`, or `sx/examples/eg`, and the provided `src`, or the
   repository root.
2. Search the Go code under `src` for repeated expression forms where a larger
   expression can be replaced by a smaller equivalent expression.
3. Write each rule as a Go `eg` template:

   ```go
   //go:build ignore

   package template

   func before(...) T { return largerExpression }
   func after(...) T  { return smallerExpression }
   ```

4. Avoid templates that duplicate, remove, or reorder expressions with side
   effects.
5. Validate the generated templates:

   ```bash
   go tool sx refactor -check -eg <dest> <src>
   ```

   Use `sx refactor -check -eg <dest> <src>` when the repository does not declare
   `sx` as a tool dependency, or `go run ./cmd/sx ...` inside the `sx` source
   tree.

6. Keep templates that parse, type-check under `eg`, and reduce AST size when
   they match.

Later `/sx min` runs search `examples/eg` and `sx/examples/eg` by default, both
under the path being minimized, its module root, and the repository root, so templates baked to
the default `dest` are picked up. Templates written elsewhere must be passed
with `-eg`.
