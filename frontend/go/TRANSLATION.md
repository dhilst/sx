# Go to `.sx` Translation

Status: implementation spec

Version: `go-translation-v0`

The Go frontend uses the standard library parser and AST packages. It emits
`.sx` and does not use an LLM.

## Roots

Each `func` declaration emits one `root` node.

Function identity:

```text
<module>/<package>.<name>
```

Method identity:

```text
<module>/<package>.<receiver>.<name>
```

Anonymous functions emit closure roots only when represented directly by the
frontend. In `go-translation-v0`, closure literals inside expressions are
represented by `closure_literal` nodes and unresolved call/state edges when
necessary; full closure body roots are reserved for a later schema-compatible
frontend version.

Top-level variable declarations with initializers emit a `package_init` root.

## Statements

The frontend maps Go statements to structural nodes:

```text
if                     -> branch + case nodes
switch/type switch     -> multi_branch + case nodes
for/range              -> loop
return                 -> return
break/continue/goto    -> jump
go f()                 -> concurrent_call
defer f()              -> defer
call expression stmt   -> call
assignment             -> assignment
short declaration      -> binding
var declaration        -> binding
other expression stmt  -> operation
```

Missing `else` arms emit a synthetic `case` node with reason
`implicit_else_case`.

## State

The frontend emits state nodes for:

```text
parameters
receivers
named results
local bindings
package-level variables
fields selected in reads/writes
external unresolved identifiers when needed
```

Reads and writes are attached to the structural node that performs the
operation. Assignments emit `data_dependency` edges from RHS state nodes to LHS
state nodes.

## Calls

Calls to functions declared in the same translated graph are resolved when the
callee is a simple identifier. Other calls are emitted as external roots with
`resolution: external`. Calls that cannot be named are emitted with
`resolution: unresolved`.

Method-call resolution across packages is intentionally not implemented in
`go-translation-v0`; method calls are explicit external relationships.

## Source Mapping

Every emitted node and edge maps to the AST span that caused it. Synthetic
implicit cases reuse the closest enclosing span and set `synthetic_reason`.
