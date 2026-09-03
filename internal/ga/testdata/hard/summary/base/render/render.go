package render

// Label renders a name for display.
func Label(name string) string {
	if name == "" {
		return "(unnamed)"
	}
	return name
}
