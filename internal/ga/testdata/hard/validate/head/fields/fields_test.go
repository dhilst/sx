package fields

import "testing"

func TestHasBlank(t *testing.T) {
	if !HasBlank("a b") || HasBlank("ab") {
		t.Fatal("HasBlank misreported whitespace")
	}
}

func TestValidate(t *testing.T) {
	count, problems := Validate([]string{"ok", "", "a b", "waytoolongvalue", "long value here"})
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
	want := []string{
		"field 1 empty",
		"field 2 has whitespace",
		"field 3 too long",
		"field 4 too long",
		"field 4 has whitespace",
	}
	if len(problems) != len(want) {
		t.Fatalf("problems = %v, want %v", problems, want)
	}
	for i := range want {
		if problems[i] != want[i] {
			t.Errorf("problems[%d] = %q, want %q", i, problems[i], want[i])
		}
	}
}
