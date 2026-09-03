package render

import "fmt"

// Label renders a name for display.
func Label(name string) string {
	if name == "" {
		return "(unnamed)"
	}
	return name
}

// Summary renders a run summary. Singular and plural wording differ, and the
// zero cases are worded differently again.
func Summary(name string, items []string, failures int) string {
	subject := "nothing to do"
	switch len(items) {
	case 0:
	case 1:
		subject = "1 item"
	default:
		subject = fmt.Sprintf("%d items", len(items))
	}
	switch failures {
	case 0:
		return fmt.Sprintf("%s: %s", Label(name), subject)
	case 1:
		return fmt.Sprintf("%s: %s, 1 failure", Label(name), subject)
	default:
		return fmt.Sprintf("%s: %s, %d failures", Label(name), subject, failures)
	}
}
