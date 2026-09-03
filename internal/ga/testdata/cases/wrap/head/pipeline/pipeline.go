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
	input := xs
	scale := factor
	return runStage(input, scale)
}

func runStage(xs []int, factor int) int {
	values := xs
	f := factor
	return applyStage(values, f)
}

func applyStage(xs []int, factor int) int {
	acc := 0
	for _, x := range xs {
		acc = accumulate(acc, x, factor)
	}
	return finish(acc)
}

func accumulate(acc, x, factor int) int {
	scaled := scaleValue(x, factor)
	next := acc + scaled
	return next
}

func scaleValue(x, factor int) int {
	product := x * factor
	return product
}

func finish(acc int) int {
	result := acc
	return result
}
