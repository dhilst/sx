package classify

// Total returns the effective score for a submission.
func Total(score, extra int) int {
	return score + extra
}

// Grade classifies a submission into a letter grade.
func Grade(score int, attended bool, extra int) string {
	if score >= 0 {
		if score <= 100 {
			if attended {
				effective := Total(score, extra)
				if effective >= 90 {
					return "A"
				} else {
					if effective >= 80 {
						return "B"
					} else {
						if effective >= 70 {
							return "C"
						} else {
							if effective >= 60 {
								return "D"
							} else {
								return "F"
							}
						}
					}
				}
			} else {
				if score >= 90 {
					return "A-"
				} else {
					if score >= 80 {
						return "B-"
					} else {
						if score >= 70 {
							return "C-"
						} else {
							if score >= 60 {
								return "D-"
							} else {
								return "F"
							}
						}
					}
				}
			}
		} else {
			return "invalid"
		}
	} else {
		return "invalid"
	}
}
