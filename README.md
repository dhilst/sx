# purgatrix

Negative pressure on the size of a Go codebase.

Writing code adds to it; nothing takes anything away. This measures the size,
finds what can be removed, removes it, and refuses to keep any change that does
not make the program smaller or that stops it working.

## The measure

```text
|AST| — the number of AST nodes it takes to express the program
```

Counting nodes rather than lines makes the measure independent of formatting and
of comments, and it has no parameters: there is nothing to tune and nothing to
validate against anyone's judgement. It is also additive — a node costs one
wherever it sits — which is what makes it usable as a target rather than only as
a ranking.

```bash
go run ./cmd/sc [-json] [-n 20] [-tests] <paths...>
```

## The transformations

The objective is `min |AST|` subject to behaviour: the same outputs and side
effects for the same inputs and state. Three things remove nodes.

| primitive | what it exploits | measured on this repository |
|---|---|---|
| **dead code** | a declaration nothing can reach | −41 in one move, the largest single win |
| **duplication** | the same code written more than once | −39 in one move |
| **inlining** | a function that only forwards | 17 moves, −2 to −27 each |

Detection is this tool's job. The transformations are not, wherever a maintained
tool already does one properly:

- **`deadcode`** answers reachability across a whole program, which a file at a
  time syntactic pass cannot.
- **`gopls`** inlines a call and extracts a function using type information —
  free variables, return values, and whether the change is legal at all.

```bash
go install golang.org/x/tools/cmd/deadcode@latest
go install golang.org/x/tools/gopls@latest

go run ./cmd/sc refactor [-apply] [-n 30] <dir>
```

Neither tool has any notion of which change is worth making. That is what the
measure supplies.

## Nothing is kept on trust

After every change the tree is re-counted, rebuilt and re-tested. A change that
does not shrink the program, or that breaks the build, or that fails the tests,
is put back.

The prediction attached to each candidate only orders the attempts. It is
routinely wrong in both directions — it said −27 for something worth −8, and
−14 for something worth −39 — so the measurement afterwards is what decides.

A failed build is read before it is judged. Removing the last user of a package
leaves its import behind, and Go will not compile that: the transformation was
unfinished, not unsafe. Unused imports are tidied and the build retried. Nothing
else is repaired, because a fix that needs to guess what the author meant is
another edit, with its own measurement and its own gate.

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

## What it does to this repository

```text
6469 -> 6182 nodes   (-287, -4.4%)
19 changes kept, 9 rejected, 1m53s, tests green throughout
```

It converges: the loop exhausts the candidates rather than running out of
rounds. That is a fixed point of these three primitives and no more — library
substitution, condition merging and unused-parameter removal are all untouched,
and each would move it again.

The largest reduction this repository has seen was not one of them. Deleting the
beam search took it from 8033 nodes to 6469 — five times what every automated
change has managed put together. No primitive could have found that: deciding a
feature does not earn its place needs a comparison against the alternative, not
a measurement of what is there.

## What it costs

`min |AST|` says a function called once is always a loss: you pay for the
declaration, the signature, the return and the call, and get one use back. So it
deletes single-use abstractions, and the fixed point has none left. Seventeen
helpers went that way in the run above.

That is the objective working, not failing, but it is the whole objective. It
knows nothing about whether the result is easier to read.
