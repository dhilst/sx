# purgatrix

A structural complexity cost for Go code.

Four things are charged, because four things make a function hard to read:

```text
how long it is        statements
how much it holds     distinct locals
how much it takes     parameters
how buried it is      nesting depth
```

## Model

Each dimension has a **free allowance** and, beyond it, costs the square of how
many times over that allowance the function is:

```text
cost = weight x (actual / allowance)^2      when actual > allowance
     = 0                                     otherwise
```

Two properties follow, and both are what the model is for:

**Ordinary code is free.** A short, flat function with two parameters costs
nothing. If everyday code were charged, the number would say nothing about
anything.

**The dimensions are comparable.** Being twice over the allowance costs the
same whether it is statements, locals or parameters. This matters more than it
sounds: charging the raw *difference* instead of the ratio made length swamp
everything, since a function is allowed 25 statements and only 3 parameters. On
`regexp/syntax` that put 89% of the score on length alone, and rated a
156-statement function at 17161 for being long against 996 for being buried
seven levels deep — exactly backwards. Charging the ratio puts the same package
at 22% length, 51% nesting, 20% locals, 4% parameters.

Defaults, all in `internal/cost`:

```text
allowance   statements 25   locals 5   parameters 3   depth 2
weight      statements 1    locals 2   parameters 3   depth 4
```

Nesting is charged on **average** depth rather than peak: one deeply buried line
is a curiosity, a function whose every statement sits four levels down is the
problem.

## Use

```bash
go run ./cmd/sc [-json] [-n 20] [-tests] <paths...>
```

```text
total 570 over 113 functions

  COST    LEN  VARS  ARGS  NEST  FUNCTION
    75     39    23     0    13  regexp/syntax/parse.go:206 parse
    55     18    18     0    19  regexp/syntax/parse.go:1132 parseClass
```

Directories are walked, skipping what the go tool itself ignores: `vendor`,
`testdata`, and directories beginning with `.` or `_`. `_test.go` files are
excluded unless `-tests` is given.

## What the model rewards

The point is to make the code shorter and easier to read, which happens two
ways: pulling duplicated code out into an abstraction, and deleting an
abstraction that was not earning its keep. The model prices both sides, so
neither is free:

- extracting a long, deeply nested block into its own function lowers the total,
  because the extracted code leaves the nesting it was buried in
- but the new function's parameters are charged, so shredding code into many
  small pieces that each need six arguments costs more than it saves

Both directions are pinned by tests in `internal/cost`.

## Status

A prototype. The weights and allowances are guesses that produce sensible
rankings on real code — on the standard library it puts `Reader.readRecord`,
`walkSymlinks` and `regexp/syntax.parse` at the top, which is where a reader
would put them — but nothing has validated them against human judgement. That
is the open question, and it is not one the tool can answer about itself.
