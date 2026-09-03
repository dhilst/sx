package pipeline

// Sum accumulates a series.
func Sum(xs []int) int {
	total := 0
	for _, x := range xs {
		total += x
	}
	return total
}

// Run scales every element by factor and accumulates the series.
func Run(xs []int, factor int) int {
	acc := 0
	for _, x := range xs {
		acc += x * factor
	}
	return acc
}
