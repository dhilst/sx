# /sx:bake [path]

Create `eg` templates for `sx` from expression patterns in the current Go
codebase.

Default output path:

```text
sx/examples/eg
```

Steps:

1. Use the provided path, or `sx/examples/eg` if no path is provided.
2. Search the codebase for repeated expression forms where a larger expression
   can be replaced by a smaller equivalent expression.
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
   sx refactor -check -eg <path> .
   ```

   If this repository is the `sx` source tree, use:

   ```bash
   go run ./cmd/sx refactor -check -eg <path> .
   ```

6. Keep templates that parse, type-check under `eg`, and reduce AST size when
   they match.

Later `/sx:min` runs search both `examples/eg` and `sx/examples/eg` by default.
