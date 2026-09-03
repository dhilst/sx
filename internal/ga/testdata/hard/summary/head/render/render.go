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
	label := Label(name)
	if len(items) == 0 {
		if failures == 0 {
			return fmt.Sprintf("%s: nothing to do", label)
		} else {
			if failures == 1 {
				return fmt.Sprintf("%s: nothing to do, 1 failure", label)
			} else {
				return fmt.Sprintf("%s: nothing to do, %d failures", label, failures)
			}
		}
	} else {
		if len(items) == 1 {
			if failures == 0 {
				return fmt.Sprintf("%s: 1 item", label)
			} else {
				if failures == 1 {
					return fmt.Sprintf("%s: 1 item, 1 failure", label)
				} else {
					return fmt.Sprintf("%s: 1 item, %d failures", label, failures)
				}
			}
		} else {
			if failures == 0 {
				return fmt.Sprintf("%s: %d items", label, len(items))
			} else {
				if failures == 1 {
					return fmt.Sprintf("%s: %d items, 1 failure", label, len(items))
				} else {
					return fmt.Sprintf("%s: %d items, %d failures", label, len(items), failures)
				}
			}
		}
	}
}
