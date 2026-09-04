package analyze

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"purgatrix/internal/sx"
)

// Edit is a source change derived from a plan, with no model involved.
//
// Not every plan can become one. Extracting duplicated code needs a typed
// signature for the new function, and the graph carries free variables but not
// their types, so that class is deliberately absent: the frontend parses and
// does not type-check, and Go infers nothing about a parameter list.
type Edit struct {
	Path      string `json:"path"`
	Before    string `json:"-"`
	After     string `json:"-"`
	Plan      Plan   `json:"plan"`
	Rationale string `json:"rationale"`
}

// EmitEdit turns a plan into a source edit using the span the graph recorded.
// It returns false when the plan is of a kind that cannot be lowered back to
// code, or when the specific shape is one this emitter refuses to touch.
func EmitEdit(root string, p Plan, read func(string) ([]string, bool)) (Edit, bool, error) {
	lines, ok := read(p.Path)
	if !ok {
		return Edit{}, false, nil
	}
	before := strings.Join(lines, "\n")
	switch p.Kind {
	case "guard_inversion":
		after, ok := emitGuardInversion(lines, p)
		if !ok {
			return Edit{}, false, nil
		}
		return Edit{
			Path: p.Path, Before: before, After: after, Plan: p,
			Rationale: "the guarded arm ends in return, break or continue, so the else is redundant and its contents can leave the nesting",
		}, true, nil
	case "dead_state":
		after, ok := emitDeadState(lines, p)
		if !ok {
			return Edit{}, false, nil
		}
		return Edit{
			Path: p.Path, Before: before, After: after, Plan: p,
			Rationale: "the value is written and never read, and the statement that writes it calls nothing, so removing it drops no effect",
		}, true, nil
	default:
		return Edit{}, false, nil
	}
}

// emitGuardInversion rewrites `} else if` into `}` followed by `if`.
//
// This is the whole edit: the guarded arm already ends in a terminator, so the
// else adds nothing, and dropping the keyword leaves the following chain at the
// same indentation. The other shape, `else { ... }`, needs the block unwrapped
// and its body unindented, which is a different and more delicate edit; this
// emitter declines it rather than guess.
func emitGuardInversion(lines []string, p Plan) (string, bool) {
	for i := p.StartLine - 1; i < len(lines) && i < p.EndLine; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(trimmed, "} else if ") {
			continue
		}
		indent := lines[i][:len(lines[i])-len(strings.TrimLeft(lines[i], " \t"))]
		rest := strings.TrimPrefix(trimmed, "} else ")
		out := append([]string(nil), lines[:i]...)
		out = append(out, indent+"}", indent+rest)
		out = append(out, lines[i+1:]...)
		return strings.Join(out, "\n"), true
	}
	return "", false
}

// emitDeadState deletes the statement that writes a value nobody reads.
func emitDeadState(lines []string, p Plan) (string, bool) {
	start, end := p.StartLine-1, p.EndLine-1
	if start < 0 || end >= len(lines) || start > end {
		return "", false
	}
	// Only a whole simple statement is safe to drop. A multi-name assignment
	// still binds the other names, and a line carrying anything else on it is
	// not this statement alone.
	stmt := strings.TrimSpace(strings.Join(lines[start:end+1], "\n"))
	if strings.Contains(stmt, ",") || !strings.Contains(stmt, ":=") {
		return "", false
	}
	out := append([]string(nil), lines[:start]...)
	out = append(out, lines[end+1:]...)
	return strings.Join(out, "\n"), true
}

// Apply writes the edit, formats the file, and hands back a function that puts
// the original text back.
func Apply(root string, e Edit) (revert func() error, err error) {
	full := filepath.Join(root, e.Path)
	original := e.Before
	if err := os.WriteFile(full, []byte(e.After), 0o644); err != nil {
		return nil, err
	}
	revert = func() error { return os.WriteFile(full, []byte(original), 0o644) }
	if out, err := exec.Command("gofmt", "-w", full).CombinedOutput(); err != nil {
		revertErr := revert()
		return nil, fmt.Errorf("gofmt rejected the edit: %w\n%s\nrevert: %v", err, string(out), revertErr)
	}
	return revert, nil
}

// CheckPrediction recompiles the edited tree and reports what the scorer
// actually charged, so an edit whose effect does not match its plan can be put
// back rather than kept on faith.
func CheckPrediction(before int, g *sx.Graph, predicted int) (measured int, ok bool, err error) {
	after, err := Verify(g)
	if err != nil {
		return 0, false, err
	}
	measured = before - after
	return measured, measured == predicted, nil
}
