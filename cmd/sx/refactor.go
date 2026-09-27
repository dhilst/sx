package main

import (
	"time"

	"github.com/dhilst/sx/internal/cost"
)

func scoreTree(dir string) (cost.Tree, error) {
	files, err := goFiles(dir, false)
	if err != nil {
		return cost.Tree{}, err
	}
	nodes := 0
	var weights []int
	for _, f := range files {
		scored, err := cost.ScoreFile(f)
		if err != nil {
			return cost.Tree{}, err
		}
		nodes += scored.Nodes
		weights = append(weights, scored.Weights()...)
	}
	return cost.Tree{Nodes: nodes, Objective: cost.Objective(nodes, weights)}, nil
}

func round(d time.Duration) string { return d.Round(time.Millisecond).String() }
