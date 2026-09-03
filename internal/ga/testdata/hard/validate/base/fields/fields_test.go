package fields

import "testing"

func TestHasBlank(t *testing.T) {
	if !HasBlank("a b") || HasBlank("ab") {
		t.Fatal("HasBlank misreported whitespace")
	}
}
