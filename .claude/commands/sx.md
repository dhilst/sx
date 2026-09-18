# /sx <cmd> [params]

Run `sx` assistant workflows.

Command shape:

```text
/sx <cmd> [params]
```

Commands:

- `min [auto|all] [path]`: minimize the current Go repository, or only the code
  under `path`, in a temporary git worktree, then review and apply accepted
  changes. Default mode is `auto`; default path is the repository root.
- `bake [dest] [src]`: create `eg` templates for `sx` from expression patterns
  found under `src`, and write them to `dest`. Default `dest` is
  `sx/examples/eg`; default `src` is the repository root.

A single parameter after `min` is the mode if it is `auto` or `all`, and the
path otherwise. A single parameter after `bake` is `dest`.

Examples:

```text
/sx min
/sx min all
/sx min ./internal/storage
/sx min auto ./internal/storage
/sx bake
/sx bake ./examples/eg
/sx bake ./examples/eg ./internal
```

Dispatch:

- For `min`, follow `.claude/commands/sx/min.md`.
- For `bake`, follow `.claude/commands/sx/bake.md`.
- If `cmd` is missing or not one of `min` or `bake`, show this command summary
  and stop.
