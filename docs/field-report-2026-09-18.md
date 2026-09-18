# Field report: sx on three real projects (2026-09-18)

sx was run over three open-source Go codebases it had never seen, in scratch
clones: nothing was pushed, opened, or filed upstream.

| Project | Target | Size (nodes) | Non-test LOC |
|---|---|---|---|
| [chenhg5/cc-connect](https://github.com/chenhg5/cc-connect) | whole module | 511,684 | 119,567 |
| [picatz/flowstate](https://github.com/picatz/flowstate) | whole module | 783,808 | 289,386 |
| [milvus-io/milvus](https://github.com/milvus-io/milvus) | `client` (a module) and `pkg/util` (a path inside the `pkg` module) | 92,350 and 202,463 | 26,062 and ~94,000 |

Each project went through `/sx min auto`, `/sx bake`, and `/sx min` again, then
a last round with `-batch`. Every kept change built, passed the project's tests,
and was reviewed.

## Results

Rounds before the last one (per-change test gate):

| Project | Rounds | Changes kept | Nodes | Lines (non-test) |
|---|---|---|---|---|
| cc-connect | 2 | 92 of 100 attempts | 511,684 → 505,878 (−5,806, −1.13%) | −1,200 (−1.00%) |
| flowstate | 1 | 28 of 32 | 783,808 → 782,927 (−881, −0.11%) | −371 (−0.13%) |
| milvus/client | 2 | 13 of 14 | 92,350 → 92,071 (−279, −0.30%) | −80 (−0.31%) |
| milvus/pkg/util | 1 | 38 of 40 | 202,463 → 199,841 (−2,622, −1.30%) | −647 |

A last round ran with `-batch`, all three projects in parallel:

| Project | Changes committed | Nodes | Lines | Outcome |
|---|---|---|---|---|
| cc-connect | 38 of 40 | 505,878 → 505,240 (−638) | −110 | final tests passed first time, squashed; 17 min |
| flowstate | 20 of 20 | 782,903 → 782,411 (−492) | −66 | final tests failed in `cmd/flow`; stopped mid-bisection at the deadline, unverified |
| milvus/pkg/util | 30 | 199,841 → 199,766 (−75) after one drop | 0 | stopped mid-bisection; the drop was wrong (see below) |

Totals over all rounds (verified changes only): cc-connect −6,444 nodes
(−1.26%) and −1,310 lines; milvus/pkg/util −2,622 (−1.30%) and −647 lines;
milvus/client −279 (−0.30%) and −80 lines; flowstate −881 (−0.11%) and −371
lines.

**A wrong drop.** milvus's tracer test fails on its own. Bisection ran the
tests through Go's test cache, so the "good" commit's pass was a replay of
the baseline, and an innocent deduplication was blamed. Bisection now runs
every step fresh (`-count=1`), as the recheck already did.

Every kept change landed on the model's prediction: across all runs, one
prediction in a hundred was off, and that was before the comment-counting fix.

### By transformation

| | Dedup | Dead code | Inline | eg rewrite | Total |
|---|---|---|---|---|---|
| Changes | 80 (47%) | 44 (26%) | 43 (25%) | 4 (2%) | 171 |
| Nodes removed | 5,871 (61%) | 2,656 (28%) | 1,029 (11%) | 32 (0.3%) | 9,588 |

Each codebase leaned on a different one: cc-connect on deduplication and
dead code (it keeps mock packages nothing calls), flowstate on inlining one-use
wrappers, milvus on deduplicating per-type conversion loops. In lines rather
than nodes, dead code counts for more (declarations take their doc comments
with them) and deduplication for less (an extraction adds a signature and a
call).

### Baked templates

`/sx bake` found five rewrites general enough for any Go code; they matched 73
times across the three projects and are now in `examples/eg`:
`len(s) == 0` → `s == ""`, `len(s) > 0` and `len(s) != 0` → `s != ""`,
`fmt.Sprintf("%d", n)` → `strconv.Itoa(n)`, and
`strconv.FormatInt(int64(n), 10)` → `strconv.Itoa(n)`.

## What the runs found in sx

The runs mattered less for the reductions than for what they exposed. Each item
below was found by a run, fixed, and pinned by an example in `test/examples` or
a unit test.

### Changes that compiled, passed the tests, and changed behaviour

- **The duplicate hash ignored tokens.** `count += n` and `count -= n` hashed
  the same, as did `+`/`-`, `break`/`continue`, and `f(x...)`/`f(x)`, so one
  was pasted over the other. This was in the original detector.
- **Extraction moved `defer` and `recover` into the helper.** cc-connect's
  config lock was released as soon as the helper returned, and the caller
  read, changed and saved the file unlocked; a response body was closed
  before the response was returned.

### Changes that could never have compiled, now refused before gopls runs

- the same text, but a variable of a different type in another copy
- a result the code after one copy needs and another's does not
- `:=` at a copy where the variables already exist
- a `return` inside functions with different result types
- a dead function the tests, or another dead function in any package, still
  calls

### Measurement and detection

- Comments parsed with a file were counted as nodes (a doc comment made one
  inline look 134 nodes smaller instead of 13).
- `Format` compared file times, and the kernel's coarse clock made it miss
  files just written; it now compares content.
- A module whose root is a package had its subpackages' tests left out of the
  gate; changes in `core/` were accepted untested and broke two test suites.
- Packages were type-checked under their name rather than their import path.
- Export data from a newer Go panicked the type checker; a package that
  cannot be type-checked is now left out instead of ending the run.

### The gate

- Tests failing before the first change blocked every change; they are now
  recorded and skipped (flowstate has six browser tests that cannot pass here).
- A test that began failing on its own mid-run was blamed on every later change
  (milvus's tracer). A failure now counts only if it goes away without the
  change, checked without Go's test cache.
- A package whose tests fail differently on every run (milvus's `paramtable`)
  is taken out of the gate after two such rechecks.
- An interrupted run left headless browsers and a half-applied change behind;
  it now stops its tests and reverts.

## Speed and memory

| | Before | After |
|---|---|---|
| One detection pass, cc-connect | 88 s, 4.2 GB | 16 s cold, ≈3 s warm |
| sx's own peak memory | ≈4 GB | 640 MB (deadcode alone still takes 4.2 GB, once a run) |
| Share of a run spent detecting | most of it | ≈10%; the project's own tests are now ≈80% |

The detection gains come from a cache keyed by file contents and dependency
export data, one `go list` per pass, `eg` matching done by the model instead of
by running `eg` per template, deadcode run once per run, and hashing each
statement once. `-batch` then removes most of the test time: on milvus/client
it produced the same 13 changes in 2m11s instead of ≈6 minutes, with two test
runs instead of fourteen.

## New in sx

- `/sx min [auto|all] [path]` and `/sx bake [dest] [src]`; templates are found
  under the path, the module, and the repository.
- `-batch`: commit each change, test once, bisect a failure, go back and detect
  again, and squash into one commit whose message gives the totals.
- A SQLite history in `.git/sx/sx.db`: runs, every test result (by name, file,
  commit and tree), and every change. A test seen both passing and failing on
  one tree is flaky and skipped from then on.
- `sx status`, `-cpuprofile`, per-detector timings, and revert messages that
  name the compiler error or the failing test.

## Next

1. **Near-duplicates.** Most remaining duplication differs in a literal or a
   name (the Feishu/Weixin variants in cc-connect's config). Extracting with
   those as parameters is the largest reduction left.
2. **Dead code in libraries.** deadcode finds nothing without a `main`; with
   `-test` the tests would be the roots.
3. **More dead code:** methods, types, constants, and unused parameters.
4. **Readable extractions:** gopls names every function `newFunction`; `auto`
   mode now renames them, and sx could propose names itself.
5. **gopls declines** some extractions inside `case` clauses ("expected '}',
   found 'case'"); worth reporting upstream.
6. The batch regression test failed once in about twenty runs and was not
   reproduced; it needs a look.
7. ~~Bisect without the test cache~~: done after the runs; every bisection
   step now runs with `-count=1`.
