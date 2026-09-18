# /sx:min [all|auto]

Minimize the current Go repository with `sx` in a temporary git worktree, then
review and apply accepted changes.

Modes:

- `all`: accept every `sx` change that passes build, test, and measurement gates.
- `auto`: inspect the diff and accept only changes that remain maintainable.

Default mode is `auto`.

Steps:

1. Find the repository root:

   ```bash
   git rev-parse --show-toplevel
   ```

2. Check for local edits:

   ```bash
   git status --short
   ```

   If the tree is dirty, tell the user before continuing. Do not silently ignore
   uncommitted or untracked files.

3. Create a disposable worktree:

   ```bash
   tmp=$(mktemp -d)
   git worktree add -d "$tmp/worktree" HEAD
   ```

4. Install helper tools if they are missing and installation is allowed:

   ```bash
   go install golang.org/x/tools/cmd/deadcode@latest
   go install golang.org/x/tools/gopls@latest
   go install golang.org/x/tools/cmd/eg@latest
   ```

5. Run `sx` in the worktree. If the repository declares `sx` as a tool
   dependency in `go.mod`, which is the preferred setup:

   ```bash
   go tool sx refactor -apply -n 100 "$tmp/worktree"
   ```

   Add it with `go get -tool github.com/dhilst/sx/cmd/sx` if it is missing and
   the user agrees to the go.mod change. Inside the `sx` source tree
   `go run ./cmd/sx` is equivalent. Otherwise fall back to an installed command:

   ```bash
   sx refactor -apply -n 100 "$tmp/worktree"
   ```

6. Run tests in the worktree. Prefer the repository's documented test command;
   otherwise use:

   ```bash
   go test ./...
   ```

7. Review:

   ```bash
   git -C "$tmp/worktree" diff
   ```

8. In `all` mode, apply the full accepted diff back to the original checkout.
   In `auto` mode, apply only changes that are worth keeping after review.

9. Remove the worktree:

   ```bash
   git worktree remove "$tmp/worktree"
   ```

Do not add a co-author trailer unless the user explicitly asks for one.
