# Structural Complexity Model

Status: normative

This document defines Structural Complexity (SC). If implementation code,
tests, fixture expectations, or other documentation disagree with this file,
this file wins.

SC is a deterministic score over a Structural Complexity Graph (SCG):

```text
SC = C_structure + C_state
```

SC is not a proof of semantic complexity, correctness, maintainability, or
behavioral equivalence. It is a deterministic measurement of the amount of
program structure and program state that a reader must navigate according to
the graph and cost functions below.

All scores are integers.

## Rule IDs

Rules are labeled `SC-*` so tests and implementation comments can cite them.

## Inputs

**SC-INPUT-1** The scorer consumes only an SCG. It must not inspect the source
language AST or source files.

**SC-INPUT-2** The frontend that builds the SCG is responsible for encoding all
information needed by this document, including node kinds, edge kinds, root
boundaries, source mappings, data relationships, call resolution status, and
synthetic graph objects.

**SC-INPUT-3** Missing, unresolved, generated, or external relationships must be
represented explicitly. They must not be silently omitted when the frontend can
observe that such a relationship exists.

## Graph Model

An SCG is:

```text
G = (N, E, R, M)
```

where:

```text
N = nodes
E = directed edges
R = roots, a subset of N
M = graph metadata
```

### Metadata

**SC-META-1** The graph metadata must include:

```text
schema_version
model_version
frontend_id
frontend_version
translation_spec_version
source_unit_id
```

**SC-META-2** `model_version` for this document is `sc-v0`.

### Source Mappings

**SC-SOURCE-1** Every node must have a source mapping.

**SC-SOURCE-2** Every edge must have a source mapping or must be marked
synthetic with a reason.

**SC-SOURCE-3** A concrete source mapping contains:

```text
path
start_line
start_column
end_line
end_column
```

Paths are relative to the analysis root and use `/` separators.

**SC-SOURCE-4** Synthetic graph objects use the closest enclosing concrete
source mapping and include:

```text
synthetic: true
synthetic_reason
```

## Nodes

A node is a typed graph object:

```text
id
kind
root_id
source
attributes
```

`kind` is one of the structural or state node kinds below. `root_id` is absent
only on root nodes before root assignment is complete.

### Root Nodes

**SC-ROOT-1** A root is an independently addressable executable or initialization
boundary.

**SC-ROOT-2** A root node has structural node kind `root` and a `root_kind`
attribute. The minimum root kinds are:

```text
function
method
closure
package_init
external_root
```

**SC-ROOT-3** A root identity must be stable across line movement when the
program identity is unchanged. It must not depend only on source line number.

**SC-ROOT-4** A root identity has this logical form:

```text
module_path + package_path + symbol_identity + root_kind
```

For anonymous closures, `symbol_identity` is:

```text
enclosing_root_identity + lexical_closure_index
```

where `lexical_closure_index` is assigned by deterministic source order within
the enclosing root.

**SC-ROOT-5** A package initialization root represents top-level executable
initialization for one package. It includes top-level variable initializers and
other language-defined initialization work that is not part of a named function
or method body.

**SC-ROOT-6** External or unresolved callable targets are represented as
`external_root` nodes. They have no local body nodes unless the frontend has a
separate source unit for them.

**SC-ROOT-7** A root node must expose these integer attributes:

```text
parameter_count
receiver_count
result_count
```

### Structural Nodes

Structural nodes represent executable structure. The valid structural node
kinds and base weights are:

| Kind | Weight | Meaning |
| --- | ---: | --- |
| `root` | 0 | Executable root boundary |
| `block` | 0 | Ordered executable region |
| `operation` | 1 | Non-call executable action or expression group |
| `binding` | 1 | Introduction of a named value |
| `assignment` | 1 | Assignment to an existing value |
| `return` | 1 | Return/yield from a root |
| `jump` | 1 | Break, continue, goto, fallthrough, or equivalent |
| `branch` | 2 | Binary conditional decision |
| `multi_branch` | 3 | Switch, match, type switch, or equivalent |
| `case` | 1 | Branch or multi-branch arm |
| `loop` | 3 | Repeated control region |
| `call` | 2 | Function, method, constructor, or callable invocation |
| `defer` | 3 | Deferred invocation |
| `concurrent_call` | 3 | Spawned asynchronous invocation |
| `closure_literal` | 2 | Closure value creation in an enclosing root |
| `panic` | 2 | Exceptional termination |
| `recover` | 2 | Exceptional control recovery |

**SC-NODE-1** A frontend may omit `block` nodes only when doing so does not
change any containment breadth or depth required by this document.

**SC-NODE-2** A frontend may group simple expression syntax into one
`operation` node. It must still emit separate `call` nodes for calls and
separate state edges for reads, writes, captures, and dependencies.

**SC-NODE-3** A construct that matches multiple node kinds uses the most
specific kind. For example, a spawned call is `concurrent_call`, not `call`.

### State Nodes

State nodes represent values or locations a reader may need to track. The valid
state node kinds and base weights are:

| Kind | Weight | Meaning |
| --- | ---: | --- |
| `parameter` | 1 | Function, method, or closure parameter |
| `receiver` | 1 | Method receiver or equivalent implicit receiver |
| `result` | 1 | Named result or explicit result slot |
| `local` | 1 | Local named value |
| `temporary` | 0 | Frontend-created temporary value |
| `constant` | 0 | Literal or compile-time constant |
| `field` | 2 | Object, struct, record, map entry, or member location |
| `captured` | 2 | Value captured from an enclosing root |
| `global` | 3 | Package/module/global mutable or readable location |
| `external_state` | 3 | State whose definition is outside the analyzed graph |

**SC-STATE-1** A value that can be read or written independently must have a
state node unless the frontend marks it as intentionally elided by translation
rules.

**SC-STATE-2** Function parameters, receivers, named results, local bindings,
captured values, globals, and independently addressable fields must be state
nodes.

**SC-STATE-3** Constants and temporaries have weight `0`, but they may appear in
data-dependency paths when needed to make dependencies explicit.

**SC-STATE-4** A state node belongs to exactly one owning root, except global and
external state nodes, which may be shared by multiple roots.

## Edges

An edge is:

```text
id
kind
from
to
source
attributes
```

### Structural Edge Kinds

| Kind | Weight | Meaning |
| --- | ---: | --- |
| `containment` | 0 | Parent structural object contains child structural object |
| `control` | 1 | Possible control transfer between structural nodes |
| `call` | 2 | Invocation from a call-like node to a root |
| `exception` | 2 | Exceptional control transfer |

### State Edge Kinds

| Kind | Weight | Meaning |
| --- | ---: | --- |
| `read` | 1 | Structural node reads a state node |
| `write` | 2 | Structural node writes a state node |
| `data_dependency` | 1 | Value of target state depends on source state |
| `capture` | 2 | Closure root captures state from an enclosing root |
| `state_escape` | 3 | State becomes reachable from outside the owning root |
| `alias` | 1 | Two state nodes may refer to the same location |

**SC-EDGE-1** `containment`, `control`, `call`, and `exception` edges are
structural edges.

**SC-EDGE-2** `read`, `write`, `data_dependency`, `capture`, `state_escape`,
and `alias` edges are state edges.

**SC-EDGE-3** `read` and `write` edges are directed from the structural node to
the state node.

**SC-EDGE-4** `data_dependency`, `capture`, `state_escape`, and `alias` edges
are directed from source state to target state, except `capture`, which is
directed from the captured outer state node to the closure-owned captured state
node.

**SC-EDGE-5** A `call` edge is directed from a `call`, `defer`, or
`concurrent_call` node to a root node.

**SC-EDGE-6** A resolved call has `resolution: resolved`. A known external call
has `resolution: external`. An unresolved call has `resolution: unresolved` and
points to an `external_root`.

**SC-EDGE-7** Duplicate edges are allowed only when they correspond to distinct
source operations. Otherwise the graph must be canonicalized to one edge.

## Root Slices

**SC-SLICE-1** A root local structural slice contains:

```text
the root node
all structural nodes reachable from the root by containment edges
```

**SC-SLICE-2** A call edge does not import the callee body into the caller's
root slice.

**SC-SLICE-3** A closure body is scored as its own root. The enclosing root pays
for the `closure_literal` node and any state/call edges directly attached to
that literal.

**SC-SLICE-4** A root local state slice contains:

```text
state nodes owned by the root
global or external state nodes read, written, captured, aliased, or depended on
by the root
```

**SC-SLICE-5** A root local state edge slice contains:

```text
read/write edges whose source structural node is in the root structural slice
data_dependency/capture/state_escape/alias edges where at least one endpoint is
owned by the root
data_dependency/capture/state_escape/alias edges where one endpoint is global or
external and the other endpoint is in the root local state slice
```

Edges between two state nodes owned only by other roots are excluded, even if
one of those nodes is global or external.

## Structural Breadth

Structural breadth measures how much sibling structure a reader must scan at
one level.

**SC-SB-1** The structural children of node `n` are nodes directly reachable
from `n` by outgoing `containment` edges, excluding state nodes.

**SC-SB-2** The general structural breadth of `n` is:

```text
SB_general(n) = max(0, count(structural_children(n)) - 1)
```

**SC-SB-3** For `branch`, breadth is the number of directly contained `case`
nodes. A missing else branch counts as an implicit synthetic `case` node.

```text
SB_branch(n) = count(case_children(n))
```

**SC-SB-4** For `multi_branch`, breadth is the number of directly contained
`case` nodes. A default case counts like any other case.

```text
SB_multi_branch(n) = count(case_children(n))
```

**SC-SB-5** For every other structural node:

```text
SB(n) = SB_general(n)
```

**SC-SB-6** For `branch` and `multi_branch`:

```text
SB(n) = SB_branch(n) or SB_multi_branch(n)
```

## Structural Depth

Structural depth measures nesting.

**SC-SD-1** Depth-increasing structural node kinds are:

```text
branch
multi_branch
case
loop
defer
concurrent_call
closure_literal
panic
recover
```

**SC-SD-2** For a structural node `n` in root `r`, structural depth is the count
of strict containment ancestors of `n` within `r` whose kind is
depth-increasing.

```text
SD_r(n) = count(depth_increasing_ancestors(n, r))
```

**SC-SD-3** The root node always has structural depth `0`.

**SC-SD-4** If multiple containment paths reach the same node, the frontend must
reject the graph as invalid unless all paths produce the same depth.

## State Breadth

State breadth measures the number of simultaneously relevant values at one
operation.

**SC-STB-1** For a structural node `n`, the directly referenced state set is:

```text
Refs(n) = unique state nodes reached by read or write edges from n
```

**SC-STB-2** The state breadth of `n` is:

```text
STB(n) = max(0, count(Refs(n)) - 1)
```

**SC-STB-3** For a state node `s`, dependency breadth is:

```text
DB(s) = count(incoming data_dependency edges to s)
```

**SC-STB-4** Alias breadth for a state node `s` is:

```text
AB(s) = count(alias edges incident to s)
```

## State Depth

State depth measures dependency-chain length.

**SC-STD-1** The state dependency graph contains state nodes and these directed
edge kinds:

```text
data_dependency
capture
state_escape
alias
```

For `alias`, include both directions.

**SC-STD-2** State input nodes are state nodes with no incoming state dependency
graph edges after SCC condensation.

**SC-STD-3** Cycles are collapsed using strongly connected components (SCCs).
Depth is computed on the resulting DAG.

**SC-STD-4** The depth of an SCC is the length of the longest directed path from
any input SCC to that SCC.

**SC-STD-5** Every state node in an SCC has the SCC depth.

## Cycles

**SC-CYCLE-1** Structural call cycles are detected in the graph formed by root
nodes and `call` edges between roots.

**SC-CYCLE-2** State cycles are detected in the state dependency graph defined
by `SC-STD-1`.

**SC-CYCLE-3** A non-trivial SCC is an SCC with more than one node or a
self-loop.

**SC-CYCLE-4** The cycle size of a non-trivial SCC is:

```text
cycle_size = node_count + internal_edge_count - 1
```

**SC-CYCLE-5** Cycle penalties are charged once per repository score, not once
per root score, unless computing an isolated single-root score.

## Hyperparameters

The default hyperparameters for `model_version = sc-v0` are:

| Name | Value |
| --- | ---: |
| `structural_depth_weight` | 1 |
| `structural_breadth_weight` | 1 |
| `state_depth_weight` | 1 |
| `state_breadth_weight` | 1 |
| `dependency_breadth_weight` | 1 |
| `alias_breadth_weight` | 1 |
| `arity_parameter_weight` | 1 |
| `arity_result_weight` | 1 |
| `excess_arity_threshold` | 3 |
| `excess_arity_weight` | 2 |
| `unresolved_call_weight` | 3 |
| `external_call_weight` | 1 |
| `structural_cycle_weight` | 4 |
| `state_cycle_weight` | 4 |

Changing any value requires a new `model_version`.

## Structural Cost

For a root `r`, let `SN_r` be the structural nodes in its local structural
slice.

**SC-CSTRUCT-1** The effective structural node weight is:

```text
EW_structure(n) = base_weight(kind(n)) * (1 + structural_depth_weight * SD_r(n))
```

**SC-CSTRUCT-2** The structural breadth cost is:

```text
BC_structure(r) =
  sum over n in SN_r of structural_breadth_weight * SB(n)
```

**SC-CSTRUCT-3** The structural edge cost is:

```text
EC_structure(r) =
  sum weights of control and exception edges whose source is in SN_r
+ sum call edge costs whose source is in SN_r
```

For call edges:

```text
resolved   => edge weight
external   => edge weight + external_call_weight
unresolved => edge weight + unresolved_call_weight
```

**SC-CSTRUCT-4** Root arity cost is:

```text
arity_count = parameter_count + receiver_count + result_count

AC(r) =
  arity_parameter_weight * (parameter_count + receiver_count)
+ arity_result_weight * result_count
+ excess_arity_weight * max(0, arity_count - excess_arity_threshold)
```

**SC-CSTRUCT-5** Root structural cost is:

```text
C_structure(r) =
  sum over n in SN_r of EW_structure(n)
+ BC_structure(r)
+ EC_structure(r)
+ AC(r)
```

The root node contributes `0` through effective structural node weight because
its base weight is `0`, but its breadth and arity still contribute.

## State Cost

For a root `r`, let `ST_r` be the state nodes in its local state slice and
`SE_r` be the state edges in its local state slice.

**SC-CSTATE-1** The effective state node weight is:

```text
EW_state(s) = base_weight(kind(s)) + state_depth_weight * STD(s)
```

**SC-CSTATE-2** Operation state breadth cost is:

```text
BC_operation_state(r) =
  sum over structural nodes n in SN_r of state_breadth_weight * STB(n)
```

**SC-CSTATE-3** Dependency breadth cost is:

```text
BC_dependency(r) =
  sum over state nodes s in ST_r of dependency_breadth_weight * max(0, DB(s) - 1)
```

**SC-CSTATE-4** Alias breadth cost is:

```text
BC_alias(r) =
  sum over state nodes s in ST_r of alias_breadth_weight * AB(s)
```

**SC-CSTATE-5** State edge cost is:

```text
EC_state(r) = sum weights of all state edges in SE_r
```

**SC-CSTATE-6** Root state cost is:

```text
C_state(r) =
  sum over s in ST_r of EW_state(s)
+ BC_operation_state(r)
+ BC_dependency(r)
+ BC_alias(r)
+ EC_state(r)
```

## Cycle Costs

**SC-CCYCLE-1** Repository structural cycle cost is:

```text
C_structural_cycles =
  structural_cycle_weight * sum cycle_size over non-trivial structural call SCCs
```

**SC-CCYCLE-2** Repository state cycle cost is:

```text
C_state_cycles =
  state_cycle_weight * sum cycle_size over non-trivial state SCCs
```

**SC-CCYCLE-3** For an isolated single-root score, include only cycles wholly
contained in that root's local slices.

## Composition

**SC-COMP-1** Root score:

```text
SC(root) = C_structure(root) + C_state(root)
```

Root scores do not include repository-level cycle costs unless the scorer is
explicitly running in isolated single-root mode.

**SC-COMP-2** File score:

```text
SC(file) = sum SC(root) for roots whose primary source mapping is in file
```

**SC-COMP-3** Repository score:

```text
SC(repo) =
  sum SC(root) for all non-external roots
+ C_structural_cycles
+ C_state_cycles
```

**SC-COMP-4** External roots are not scored as repository roots. Their cost is
charged through call, state, unresolved, and external penalties from analyzed
roots.

**SC-COMP-5** Generated files are scored unless the analysis configuration
explicitly excludes them. The exclusion configuration is part of the score
identity.

## PR Score Semantics

**SC-PR-1** For a base revision `B` and head revision `H` analyzed with the
same model, frontend, translation spec, and configuration:

```text
Score(PR) = SC(H) - SC(B)
```

**SC-PR-2** Interpretation:

```text
+N => PR increases structural complexity by N
 0 => PR is structurally neutral
-N => PR decreases structural complexity by N
```

**SC-PR-3** Added files and roots contribute positive score through `SC(H)`.
Deleted files and roots contribute negative score through their absence from
`SC(H)`.

**SC-PR-4** A PR score is invalid if base and head are not analyzed with the
same `model_version`, frontend identity, translation spec version, and analysis
configuration.

**SC-PR-5** SC does not define behavioral equivalence. A lower PR score is an
optimization candidate only if configured DBT or tests observe no behavioral
difference.

## Hotspots

**SC-HOTSPOT-1** A node contribution is:

```text
node_contribution =
  effective node weight
+ breadth cost charged at the node
+ state breadth cost charged at the node
+ edge costs charged from the node
```

**SC-HOTSPOT-2** A state node contribution is:

```text
state_node_contribution =
  effective state node weight
+ dependency breadth cost charged at the state node
+ alias breadth cost charged at the state node
+ state edge costs charged from or to the state node
```

**SC-HOTSPOT-3** Hotspots are reported by descending contribution. Ties are
broken by source path, start line, start column, node kind, then node id.

## Validation Requirements

An SCG is invalid if any of these conditions hold:

**SC-VAL-1** A non-synthetic node has no concrete source mapping.

**SC-VAL-2** A non-synthetic edge has no concrete source mapping.

**SC-VAL-3** A node kind or edge kind is unknown for the graph's
`model_version`.

**SC-VAL-4** A structural node is reachable by containment from two different
roots.

**SC-VAL-5** A root containment slice has inconsistent structural depths as
defined by `SC-SD-4`.

**SC-VAL-6** A `call` edge does not originate from `call`, `defer`, or
`concurrent_call`.

**SC-VAL-7** A `read` or `write` edge does not originate from a structural node
or does not target a state node.

**SC-VAL-8** A `data_dependency`, `capture`, `state_escape`, or `alias` edge
does not connect state nodes.

**SC-VAL-9** A root identity is missing or duplicated.

**SC-VAL-10** Required metadata from `SC-META-1` is missing.

## Worked Examples

The examples use abstract SCGs rather than language syntax. They are intended
as scorer fixtures.

### Example 1: One Operation, No State

Graph:

```text
nodes:
  r root(function), parameters=0, results=0
  n operation

edges:
  r -containment-> n
```

Structure:

```text
EW(r) = 0
EW(n) = 1 * (1 + 1 * 0) = 1
SB(r) = max(0, 1 - 1) = 0
EC = 0
AC = 0
C_structure = 1
```

State:

```text
C_state = 0
```

Total:

```text
SC = 1
```

### Example 2: Four Sibling Operations

Graph:

```text
nodes:
  r root(function), parameters=0, results=0
  a operation
  b operation
  c operation
  d operation

edges:
  r -containment-> a
  r -containment-> b
  r -containment-> c
  r -containment-> d
```

Structure:

```text
node weights = 4 * 1 = 4
SB(r) = max(0, 4 - 1) = 3
EC = 0
AC = 0
C_structure = 7
```

State:

```text
C_state = 0
```

Total:

```text
SC = 7
```

### Example 3: Binary Branch With One Operation Per Arm

Graph:

```text
nodes:
  r root(function), parameters=0, results=0
  br branch
  yes case
  no case
  a operation
  b operation

edges:
  r  -containment-> br
  br -containment-> yes
  br -containment-> no
  yes -containment-> a
  no  -containment-> b
```

Depth:

```text
SD(br) = 0
SD(yes) = 1   because branch is a strict depth-increasing ancestor
SD(no)  = 1
SD(a)   = 2   because branch and case are strict depth-increasing ancestors
SD(b)   = 2
```

Structure:

```text
EW(br)  = 2 * (1 + 0) = 2
EW(yes) = 1 * (1 + 1) = 2
EW(no)  = 1 * (1 + 1) = 2
EW(a)   = 1 * (1 + 2) = 3
EW(b)   = 1 * (1 + 2) = 3
SB(r)   = 0
SB(br)  = 2
SB(yes) = 0
SB(no)  = 0
EC = 0
AC = 0
C_structure = 14
```

State:

```text
C_state = 0
```

Total:

```text
SC = 14
```

### Example 4: Function Arity

Graph:

```text
nodes:
  r root(function), parameters=5, receivers=0, results=1
```

Structure:

```text
arity_count = 5 + 0 + 1 = 6
AC =
  1 * 5
+ 1 * 1
+ 2 * max(0, 6 - 3)
= 12
C_structure = 12
```

State:

The six parameter/result state nodes are still scored as state:

```text
5 parameter nodes: 5 * (1 + 0) = 5
1 result node:     1 * (1 + 0) = 1
C_state = 6
```

Total:

```text
SC = 18
```

### Example 5: Linear State Dependency Chain

Graph:

```text
state nodes:
  p parameter
  x local
  y local

state edges:
  p -data_dependency-> x
  x -data_dependency-> y
```

State depth:

```text
STD(p) = 0
STD(x) = 1
STD(y) = 2
```

State:

```text
EW(p) = 1 + 0 = 1
EW(x) = 1 + 1 = 2
EW(y) = 1 + 2 = 3
EC_state = 2 data_dependency edges * 1 = 2
C_state = 8
```

Structure:

```text
C_structure = 0
```

Total:

```text
SC = 8
```

### Example 6: Broader State Operation

Graph:

```text
structural nodes:
  op operation

state nodes:
  a parameter
  b parameter
  c parameter
  out local

state edges:
  op -read-> a
  op -read-> b
  op -read-> c
  op -write-> out
  a -data_dependency-> out
  b -data_dependency-> out
  c -data_dependency-> out
```

State breadth:

```text
Refs(op) = {a, b, c, out}
STB(op) = max(0, 4 - 1) = 3
DB(out) = 3
dependency breadth for out = max(0, 3 - 1) = 2
```

State depth:

```text
STD(a) = 0
STD(b) = 0
STD(c) = 0
STD(out) = 1
```

State:

```text
state node costs = 1 + 1 + 1 + 2 = 5
read/write edge costs = 1 + 1 + 1 + 2 = 5
data_dependency edge costs = 3
operation state breadth = 3
dependency breadth = 2
C_state = 18
```

Structure:

```text
op contributes 1
C_structure = 1
```

Total:

```text
SC = 19
```

### Example 7: PR Delta

Base repository:

```text
SC(base) = 120
```

Head repository:

```text
SC(head) = 145
```

PR score:

```text
Score(PR) = 145 - 120 = +25
```

Interpretation:

```text
The PR increases SC by 25.
```

If a candidate rewrite of the same PR scores:

```text
SC(candidate head) = 132
```

then:

```text
Score(candidate PR) = 132 - 120 = +12
Improvement = +12 - +25 = -13
```

The candidate is structurally lower by 13, but it is acceptable only if the
configured behavioral oracle observes no behavioral difference from the
original PR.

## Determinism

**SC-DET-1** Given byte-identical SCG input and the same `model_version`, a
scorer must produce byte-identical reports after canonical serialization.

**SC-DET-2** All unordered graph traversals must use deterministic ordering:

```text
source path
start line
start column
kind
stable identity if present
node or edge id
```

**SC-DET-3** The scorer must not use wall-clock time, filesystem traversal
order, map iteration order, random numbers, network access, or source language
parsing.

## Non-Goals

**SC-NONGOAL-1** SC does not prove behavior preservation.

**SC-NONGOAL-2** SC does not estimate runtime performance.

**SC-NONGOAL-3** SC does not infer programmer intent.

**SC-NONGOAL-4** SC does not rank code style preferences except where they are
encoded in this document's graph and cost rules.
