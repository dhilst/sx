package ga

import "purgatrix/internal/suggest"

// SeedPrompts is generation 0: the incumbent prompt plus four hand-written
// rivals that pull in deliberately different directions, so the first round
// measures a spread instead of five paraphrases.
func SeedPrompts() []Individual {
	return []Individual{
		{ID: "incumbent", Origin: "seed", Prompt: suggest.DefaultPrompt()},
		{ID: "mechanic", Origin: "seed", Prompt: seedMechanic},
		{ID: "surgeon", Origin: "seed", Prompt: seedSurgeon},
		{ID: "verifier", Origin: "seed", Prompt: seedVerifier},
		{ID: "hotspot", Origin: "seed", Prompt: seedHotspot},
	}
}

const seedMechanic = `You simplify Go code so that sx's measured Structural Complexity drops while observable behaviour stays identical.

sx charges every structural node its kind weight times one plus its nesting depth, so depth is the most expensive thing in the graph. Apply these rewrites where the hotspot list points:

- Turn nested if/else pyramids into guard clauses that return early.
- Collapse else-if ladders into one flat switch; in Go an else-if is a nested if, so a six-arm ladder is six levels deep.
- Hoist a guard that is duplicated in every arm above the dispatch.
- Delete pass-through functions that only forward their arguments, and inline their bodies into the single caller.
- Drop intermediate locals that are assigned once and used once.
- Reduce parameter and result counts where a value is already reachable.

Never touch _test.go files. Never change an exported signature, a printed byte, or an error string. Do not compute or mention SC numbers; sx measures them.

Prefer two or three independent rewrites over one sweeping one, and make each rewrite complete: the file you return must compile on its own.`

const seedSurgeon = `Simplify the hotspot files. Nothing else.

Rules:
1. Read the hotspot list. Work only where it points.
2. Make each candidate the smallest complete change that removes real structure: one flattened pyramid, one collapsed ladder, one deleted wrapper.
3. Emit several independent candidates rather than one large one. Each is applied alone.
4. Behaviour is frozen. Same printed bytes, same exit status, same exported signatures, same error text, same guard evaluation order.
5. Never edit tests. Never add an abstraction, an interface, a helper type, or a comment block to compensate.
6. sx owns all scores. Do not compute, estimate, or discuss them.

If a file has no structure worth removing, leave it alone and say so by returning no candidate for it.`

const seedVerifier = `Before you rewrite anything, establish what must not change.

Step 1. For each hotspot file, list the observable contract: exported signatures, every printed string, every error message, the order in which guards fire, and the value returned for boundary inputs.
Step 2. Find the structure that costs the most: the deepest nesting, the longest else-if ladder, the functions that only forward to another function, the locals that exist for one use.
Step 3. Rewrite to remove that structure with guard clauses, flat switches, hoisted guards, and inlined pass-throughs.
Step 4. Re-check the rewrite against your Step 1 list, input by input, including negative, zero, and out-of-range values. If any answer changes, discard that rewrite.

Never modify _test.go files. Never compute or restate SC scores; sx measures them and your estimate would be wrong.

Return complete files that compile. A rewrite you cannot verify against Step 1 is not worth returning.`

const seedHotspot = `Attack the most expensive structure in the program.

The hotspot list is ordered by measured contribution. Take the top entry, find the function that contains it, and rewrite that whole function into its simplest form: early returns instead of nested branches, one flat switch instead of an else-if ladder, one loop body instead of a chain of forwarding calls, no locals that exist only to be passed along.

Then do the same for the next hotspot in a separate candidate, so each candidate stands or falls alone.

Constraints that override any instinct to be clever:
- identical observable behaviour: printed bytes, exit status, error strings, exported signatures
- no edits to _test.go files
- no new abstractions, no new indirection, no formatting-only churn
- sx computes every score; never produce a number yourself

Deliver complete, compilable file contents.`
