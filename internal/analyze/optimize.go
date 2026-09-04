package analyze

import (
	"fmt"

	"purgatrix/internal/sx"
)

// Step is one move along the trajectory: the plan taken, and what the scorer
// charged before and after taking it.
type Step struct {
	Round  int    `json:"round"`
	Plan   Plan   `json:"plan"`
	Before int    `json:"before"`
	After  int    `json:"after"`
	Saving int    `json:"saving"`
	Note   string `json:"note,omitempty"`
}

// Trajectory is IR(0) -> IR(1) -> ... -> IR(N): the sequence of rewrites that
// walks the graph down to a fixed point, and what each one was worth.
type Trajectory struct {
	Initial   int    `json:"initial"`
	Final     int    `json:"final"`
	Saving    int    `json:"saving"`
	Rounds    int    `json:"rounds"`
	Converged bool   `json:"converged"`
	Steps     []Step `json:"steps"`
	Stopped   string `json:"stopped,omitempty"`
}

// realizable reports whether a plan kind corresponds to an edit somebody could
// actually make.
//
// excess_arity does not. Its rewrite drops the arity charge by editing the
// count, but a real edit has to put those parameters somewhere - a struct, with
// fields, built at every call site - and that destination costs at least as
// much as the charge removed. Measured twice against this codebase: once the
// tests broke, once the score did not move. A trajectory built from moves that
// cannot be made is a fiction, so the optimizer does not take them.
func realizable(kind string) bool {
	switch kind {
	case "dead_state", "guard_inversion", "duplicate_structure":
		return true
	default:
		return false
	}
}

// Optimize walks the graph toward a fixed point, taking the most valuable
// realizable rewrite each round and re-analysing the result. It converges when
// no remaining plan lowers the score.
//
// The trajectory is a bound and an ordering, not a patch. Each step is exact
// for the graph; whether the corresponding source edit preserves behaviour is
// still decided by tests and the behaviour test.
func Optimize(g *sx.Graph, maxRounds int) (Trajectory, error) {
	if maxRounds <= 0 {
		maxRounds = 25
	}
	initial, err := Verify(g)
	if err != nil {
		return Trajectory{}, err
	}
	traj := Trajectory{Initial: initial, Final: initial}
	current := g
	seen := map[string]int{}

	for round := 1; round <= maxRounds; round++ {
		report, err := Analyze(current)
		if err != nil {
			traj.Stopped = fmt.Sprintf("analysis failed at round %d: %v", round, err)
			break
		}
		best, ok := bestRealizable(report.Plans)
		if !ok {
			traj.Converged = true
			break
		}
		// A plan that keeps reappearing without shrinking the score means the
		// rewrite is not making progress; stop rather than spin.
		key := best.Kind + "@" + best.Anchor
		if seen[key] > 1 {
			traj.Stopped = "a plan repeated without lowering the score"
			break
		}
		seen[key]++

		before, err := Verify(current)
		if err != nil {
			traj.Stopped = err.Error()
			break
		}
		next, err := Rewrite(current, best)
		if err != nil {
			traj.Stopped = fmt.Sprintf("rewrite failed at round %d: %v", round, err)
			break
		}
		after, err := Verify(next)
		if err != nil {
			traj.Stopped = fmt.Sprintf("rewritten graph did not score at round %d: %v", round, err)
			break
		}
		if after >= before {
			traj.Converged = true
			break
		}
		traj.Steps = append(traj.Steps, Step{
			Round: round, Plan: best, Before: before, After: after, Saving: before - after,
		})
		traj.Rounds = round
		traj.Final = after
		current = next
	}
	traj.Saving = traj.Initial - traj.Final
	return traj, nil
}

// bestRealizable picks the most valuable plan the optimizer is willing to take.
func bestRealizable(plans []Plan) (Plan, bool) {
	best := Plan{}
	found := false
	for _, p := range plans {
		if !realizable(p.Kind) || !p.Verified || p.Exact <= 0 {
			continue
		}
		if !found || p.Exact > best.Exact {
			best, found = p, true
		}
	}
	return best, found
}
