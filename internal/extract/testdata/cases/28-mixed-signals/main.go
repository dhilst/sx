package main

import "fmt"

func scan(rows [][]int) (sum int, err error) {
outer:
	for r, row := range rows {
		for _, v := range row {
			if v == 0 { //<
				continue outer
			}
			if v < 0 {
				err = fmt.Errorf("row %d: negative", r)
				return
			}
			if v > 100 {
				break outer
			}
			sum += v //>
		}
	}
	return sum, err
}

func main() {
	fmt.Println(scan([][]int{{1, 2}, {0, 9}, {3}}))
	fmt.Println(scan([][]int{{1}, {2, -5}}))
	fmt.Println(scan([][]int{{1}, {500, 2}, {7}}))
}
