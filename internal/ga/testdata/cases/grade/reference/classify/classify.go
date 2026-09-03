package classify

// Total returns the effective score for a submission.
func Total(score, extra int) int {
	return score + extra
}

// Grade classifies a submission into a letter grade.
func Grade(score int, attended bool, extra int) string {
	if score < 0 || score > 100 {
		return "invalid"
	}
	effective := score
	suffix := "-"
	if attended {
		effective = Total(score, extra)
		suffix = ""
	}
	switch {
	case effective >= 90:
		return "A" + suffix
	case effective >= 80:
		return "B" + suffix
	case effective >= 70:
		return "C" + suffix
	case effective >= 60:
		return "D" + suffix
	default:
		return "F"
	}
}
