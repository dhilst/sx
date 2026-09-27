package refactor

import "fmt"

// Deleting an unreachable function changes the tree by
//
//	ΔN = −N(D) + ΔI
//
// D is the declaration and ΔI the imports only it used, which the repair
// step removes with it (−N(spec) each, and −1 for an import declaration left
// empty). Its doc comment goes too, but comments are not nodes.

// Removal is the model's account of deleting a declaration.
type Removal struct {
	D, I int
}

// Delta is the change in |AST|.
func (m Removal) Delta() int { return -m.D + m.I }

func (m Removal) String() string {
	return fmt.Sprintf("D=%d ΔI=%+d ΔN=%+d", m.D, m.I, m.Delta())
}
