package classify

// Total returns the effective score for a submission.
func Total(score, extra int) int {
	return score + extra
}
