package main

import (
	"time"

	"purgatrix/internal/cost"
)

func scoreTree(dir string) (int, error) {
	files, err := goFiles(dir, false)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, f := range files {
		scored, err := cost.ScoreFile(f)
		if err != nil {
			return 0, err
		}
		total += scored.Nodes
	}
	return total, nil
}

func round(d time.Duration) string { return d.Round(time.Millisecond).String() }
