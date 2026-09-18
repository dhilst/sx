# /sx <cmd> [param]

Run `sx` assistant workflows.

Command shape:

```text
/sx <cmd> [param]
```

Commands:

- `min [auto|all]`: minimize the current Go repository in a temporary git
  worktree, then review and apply accepted changes. Default mode is `auto`.
- `bake [path]`: create `eg` templates for `sx` from expression patterns in the
  current Go codebase. Default path is `sx/examples/eg`.

Examples:

```text
/sx min auto
/sx min all
/sx bake
/sx bake ./examples/eg
```

Dispatch:

- For `min`, follow `.claude/commands/sx/min.md`.
- For `bake`, follow `.claude/commands/sx/bake.md`.
- If `cmd` is missing or not one of `min` or `bake`, show this command summary
  and stop.

