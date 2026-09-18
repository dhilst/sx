package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/dhilst/sx/internal/refactor"
)

// verifyBatch tests a batched run once. When a test fails that passed at the
// start, it bisects the failure to the commit that caused it and returns
// that commit; a failure the commit does not reproduce when checked again is
// flaky, joins the baseline, and the run is tested again.
func verifyBatch(stdout io.Writer, hist *refactor.History, scope refactor.Scope, baseline refactor.Failures,
	testTime *time.Duration, testRuns *int) (bad string, why error, err error) {
	failing := func(recheck bool) (refactor.Failures, error) {
		start := time.Now()
		defer func() { *testTime += time.Since(start); *testRuns++ }()
		test := scope.Failures
		if recheck {
			test = scope.Recheck
		}
		f, err := test(baseline)
		if err != nil {
			return nil, err
		}
		return f, f.Since(baseline)
	}
	for {
		commits := hist.Commits()
		if len(commits) == 0 {
			return "", nil, nil
		}
		fails, why := failing(false)
		if why == nil {
			fmt.Fprintf(stdout, "  tests pass with all %d changes\n", len(commits))
			return "", nil, nil
		}
		fmt.Fprintf(stdout, "  tests fail with the changes (%v); bisecting %d commits\n", why, len(commits))
		bad, err := hist.Bisect(commits, func() bool {
			_, why := failing(false)
			return why != nil
		})
		if err != nil {
			return "", nil, err
		}
		// Bisection believes the result it gets. A flaky test sends it to
		// a commit at random, so the verdict is checked again, fresh, before
		// a change is thrown away.
		if err := hist.Checkout(bad); err != nil {
			return "", nil, err
		}
		_, confirmed := failing(true)
		if err := hist.Checkout(commits[len(commits)-1]); err != nil {
			return "", nil, err
		}
		if confirmed != nil {
			return bad, why, nil
		}
		var names []string
		for k := range fails {
			if !baseline[k] {
				baseline[k] = true
				pkg, test, _ := strings.Cut(k, "\x00")
				names = append(names, strings.TrimSpace(pkg+" "+test))
			}
		}
		sort.Strings(names)
		fmt.Fprintf(stdout, "      (%s did not fail again at %s: flaky, skipped from now on)\n", strings.Join(names, ", "), short(bad))
	}
}

// finishBatch squashes a verified run into one commit that says what it did,
// keeping the separate commits under refs/sx/runs, and records its totals.
func finishBatch(stdout io.Writer, dir string, hist *refactor.History, attempts, dropped int,
	spent map[string]time.Duration, testTime time.Duration, testRuns int) (int, error) {
	kept := hist.Commits()
	m := refactor.RunMetrics{
		Attempts: attempts, Kept: len(kept), Dropped: dropped, ByKind: map[refactor.Kind]int{},
		Apply: spent["apply+gate"], Test: testTime, TestRuns: testRuns,
	}
	for _, d := range []string{"load", "dead", "inline", "dedup", "eg"} {
		m.Detect += spent[d]
	}
	for _, c := range kept {
		m.ByKind[hist.KindOf(c)]++
	}
	m.NodesAfter, _ = scoreTree(dir)
	if err := hist.Checkout(hist.Start); err == nil {
		m.NodesBefore, _ = scoreTree(dir)
		if len(kept) > 0 {
			hist.Checkout(kept[len(kept)-1])
		}
	}
	m.LOCAdded, m.LOCRemoved = hist.LOC()
	summary := fmt.Sprintf("sx: %d changes, %d -> %d nodes (%+d), %+d lines\n\n", len(kept), m.NodesBefore, m.NodesAfter,
		m.NodesAfter-m.NodesBefore, m.LOCAdded-m.LOCRemoved)
	for _, k := range []refactor.Kind{refactor.KindDuplicate, refactor.KindDead, refactor.KindInline, refactor.KindEg} {
		if n := m.ByKind[k]; n > 0 {
			summary += fmt.Sprintf("  %-7s %d\n", k, n)
		}
	}
	summary += fmt.Sprintf("\nEach change is in refs/sx/runs/%s; sx status lists the runs.\n", hist.Run)
	if err := hist.Squash(summary); err != nil {
		return 0, err
	}
	fmt.Fprintf(stdout, "  squashed %d changes into one commit; the separate commits are kept in refs/sx/runs/%s\n", len(kept), hist.Run)
	return len(kept), hist.Close(m)
}

func short(commit string) string {
	if len(commit) > 8 {
		return commit[:8]
	}
	return commit
}

// status prints the runs recorded for the repository dir is in: nodes and
// lines before and after, and what each run kept.
func status(stdout io.Writer, dir string) error {
	runs, err := refactor.Runs(dir)
	if err != nil {
		return err
	}
	if len(runs) == 0 {
		fmt.Fprintln(stdout, "no runs recorded; sx refactor -apply -batch records them")
		return nil
	}
	fmt.Fprintf(stdout, "%-19s %9s %9s %8s %7s %6s %6s %5s %5s %5s %4s %8s\n",
		"RUN", "NODES", "AFTER", "ΔNODES", "ΔNODES%", "ΔLOC", "KEPT", "DEDUP", "DEAD", "INLN", "EG", "TIME")
	for _, r := range runs {
		pct := 0.0
		if r.NodesBefore > 0 {
			pct = 100 * float64(r.NodesAfter-r.NodesBefore) / float64(r.NodesBefore)
		}
		fmt.Fprintf(stdout, "%-19s %9d %9d %+8d %6.2f%% %+6d %3d/%-2d %5d %5d %5d %4d %8s\n",
			r.Run, r.NodesBefore, r.NodesAfter, r.NodesAfter-r.NodesBefore, pct, r.LOCAdded-r.LOCRemoved,
			r.Kept, r.Attempts, r.Dedup, r.Dead, r.Inline, r.Eg, round(r.Duration))
	}
	return nil
}
