package config

import "testing"

func TestNormalize(t *testing.T) {
	if got := Normalize("  a  "); got != "a" {
		t.Fatalf("Normalize = %q, want \"a\"", got)
	}
}

func TestParse(t *testing.T) {
	values, problems := Parse([]string{
		"",
		"# a comment",
		"host = example.invalid",
		"port=8080",
		"host = other",
		"empty =",
		" = novalue",
		"novalue",
		"=leading",
	})
	want := map[string]string{"host": "example.invalid", "port": "8080"}
	if len(values) != len(want) {
		t.Fatalf("values = %v, want %v", values, want)
	}
	for k, v := range want {
		if values[k] != v {
			t.Errorf("values[%q] = %q, want %q", k, values[k], v)
		}
	}
	wantProblems := []string{
		`line 5: duplicate key "host"`,
		`line 6: empty value for "empty"`,
		"line 7: empty key",
		"line 8: missing '='",
		"line 9: missing '='",
	}
	if len(problems) != len(wantProblems) {
		t.Fatalf("problems = %v, want %v", problems, wantProblems)
	}
	for i, p := range wantProblems {
		if problems[i] != p {
			t.Errorf("problems[%d] = %q, want %q", i, problems[i], p)
		}
	}
}
