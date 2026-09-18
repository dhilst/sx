package refactor

import (
	"bytes"
	"database/sql"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// History is a batched run's record in git. Each kept change is a commit, so
// the tests can run once at the end and a failure can be bisected to the
// change that caused it, instead of the whole suite running after every
// change - on flowstate three minutes an attempt.
//
// What the run learns is kept in a SQLite database under the repository's git
// directory, out of the working tree (see schema): every test result, by the
// commit and tree it ran on and the file the test is in; every change with
// its measured size; and each run's totals. A test that has both passed and
// failed on one tree is flaky, whatever the change, and later runs skip it
// from the start.
type History struct {
	dir   string // where sx runs; inside a git work tree
	db    *sql.DB
	Run   string
	Start string // the commit the run started from

	testFiles map[string]map[string]string // package -> test -> file
}

// schema is the database. Tests are indexed by name, by file and by commit,
// and by tree for the flakiness question.
const schema = `
CREATE TABLE IF NOT EXISTS runs (
	run TEXT PRIMARY KEY, dir TEXT, start_commit TEXT, end_commit TEXT,
	started_at TEXT, finished_at TEXT,
	nodes_before INTEGER, nodes_after INTEGER, loc_added INTEGER, loc_removed INTEGER,
	attempts INTEGER, kept INTEGER, dropped INTEGER,
	dead INTEGER, inline INTEGER, dedup INTEGER, eg INTEGER,
	detect_seconds REAL, apply_seconds REAL, test_seconds REAL, test_runs INTEGER
);
CREATE TABLE IF NOT EXISTS tests (
	run TEXT, commit_hash TEXT, tree TEXT, package TEXT, test TEXT, file TEXT,
	outcome TEXT, elapsed REAL
);
CREATE INDEX IF NOT EXISTS tests_by_name ON tests (test);
CREATE INDEX IF NOT EXISTS tests_by_file ON tests (file);
CREATE INDEX IF NOT EXISTS tests_by_commit ON tests (commit_hash);
CREATE INDEX IF NOT EXISTS tests_by_tree ON tests (tree, package, test);
CREATE TABLE IF NOT EXISTS changes (
	run TEXT, commit_hash TEXT, kind TEXT, target TEXT, predicted INTEGER,
	nodes_before INTEGER, nodes_after INTEGER, loc_added INTEGER, loc_removed INTEGER,
	dropped TEXT
);
CREATE INDEX IF NOT EXISTS changes_by_commit ON changes (commit_hash);
CREATE INDEX IF NOT EXISTS changes_by_run ON changes (run);
`

// RunMetrics is a run's totals.
type RunMetrics struct {
	NodesBefore, NodesAfter, LOCAdded, LOCRemoved int
	Attempts, Kept, Dropped                       int
	ByKind                                        map[Kind]int
	Detect, Apply, Test                           time.Duration
	TestRuns                                      int
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), firstLine(stderr.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// commitArgs commits without signing or hooks, as the user if git knows who
// that is and as sx otherwise; the commits are squashed at the end.
func commitArgs(dir string, rest ...string) []string {
	args := []string{"-c", "commit.gpgsign=false"}
	if email, _ := git(dir, "config", "user.email"); email == "" {
		args = append(args, "-c", "user.name=sx", "-c", "user.email=sx@localhost")
	}
	return append(append(args, "commit", "--no-verify", "-q"), rest...)
}

// OpenHistory starts a batched run in the git work tree dir is in. The tree
// must be clean: every commit has to be the one change it says it is.
func OpenHistory(dir string) (*History, error) {
	if status, err := git(dir, "status", "--porcelain", "--untracked-files=no"); err != nil {
		return nil, err
	} else if status != "" {
		return nil, fmt.Errorf("batched testing needs a clean git tree; commit or stash first")
	}
	start, err := git(dir, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	db, err := openStore(dir)
	if err != nil {
		return nil, err
	}
	h := &History{dir: dir, db: db, Run: time.Now().Format("20060102-150405.000"), Start: start, testFiles: map[string]map[string]string{}}
	_, err = db.Exec(`INSERT INTO runs (run, dir, start_commit, started_at) VALUES (?, ?, ?, ?)`,
		h.Run, dir, start, time.Now().Format(time.RFC3339))
	return h, err
}

// openStore opens <git dir>/sx/sx.db, creating it on first use.
func openStore(dir string) (*sql.DB, error) {
	common, err := git(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, err
	}
	store := filepath.Join(common, "sx")
	if err := os.MkdirAll(store, 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(store, "sx.db"))
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// Close finishes the run's record with its totals.
func (h *History) Close(m RunMetrics) error {
	end, _ := h.Head()
	_, err := h.db.Exec(`UPDATE runs SET end_commit=?, finished_at=?, nodes_before=?, nodes_after=?,
		loc_added=?, loc_removed=?, attempts=?, kept=?, dropped=?, dead=?, inline=?, dedup=?, eg=?,
		detect_seconds=?, apply_seconds=?, test_seconds=?, test_runs=? WHERE run=?`,
		end, time.Now().Format(time.RFC3339), m.NodesBefore, m.NodesAfter, m.LOCAdded, m.LOCRemoved,
		m.Attempts, m.Kept, m.Dropped, m.ByKind[KindDead], m.ByKind[KindInline], m.ByKind[KindDuplicate], m.ByKind[KindEg],
		m.Detect.Seconds(), m.Apply.Seconds(), m.Test.Seconds(), m.TestRuns, h.Run)
	h.db.Close()
	return err
}

// Head is the current commit and its tree.
func (h *History) Head() (commit, tree string) {
	commit, _ = git(h.dir, "rev-parse", "HEAD")
	tree, _ = git(h.dir, "rev-parse", "HEAD^{tree}")
	return commit, tree
}

// Recorder is what Scope.Record takes: every result is written with the
// commit and tree it ran on, and the file the test is declared in.
func (h *History) Recorder() func(pkg, test, outcome string, elapsed float64) {
	return func(pkg, test, outcome string, elapsed float64) {
		commit, tree := h.Head()
		top, _, _ := strings.Cut(test, "/")
		h.db.Exec(`INSERT INTO tests (run, commit_hash, tree, package, test, file, outcome, elapsed) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			h.Run, commit, tree, pkg, test, h.testFile(pkg, top), outcome, elapsed)
	}
}

// testFile is the file, relative to the repository, that declares a test.
func (h *History) testFile(pkg, test string) string {
	files, ok := h.testFiles[pkg]
	if !ok {
		files = map[string]string{}
		h.testFiles[pkg] = files
		dir, err := goList(h.dir, "-f", "{{.Dir}}", pkg)
		root, _ := git(h.dir, "rev-parse", "--show-toplevel")
		if err == nil {
			paths, _ := filepath.Glob(filepath.Join(dir, "*_test.go"))
			for _, path := range paths {
				f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
				if err != nil {
					continue
				}
				rel, err := filepath.Rel(root, path)
				if err != nil {
					rel = path
				}
				for _, d := range f.Decls {
					if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil {
						files[fn.Name.Name] = rel
					}
				}
			}
		}
	}
	return files[test]
}

// Commit records a kept change as a commit of everything under the run's
// directory, and its sizes.
func (h *History) Commit(c Candidate, before, after int) error {
	if _, err := git(h.dir, "add", "-A", "."); err != nil {
		return err
	}
	msg := fmt.Sprintf("sx: %s %s (%d -> %d nodes)\n\nsx-key: %s", c.Kind, c.Target, before, after, c.Key())
	if _, err := git(h.dir, commitArgs(h.dir, "-m", msg)...); err != nil {
		return err
	}
	commit, _ := h.Head()
	added, removed := h.loc(commit+"^", commit)
	_, err := h.db.Exec(`INSERT INTO changes (run, commit_hash, kind, target, predicted, nodes_before, nodes_after, loc_added, loc_removed)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, h.Run, commit, string(c.Kind), c.Target, c.Predicted, before, after, added, removed)
	return err
}

// loc is the lines of non-test Go added and removed between two commits.
func (h *History) loc(from, to string) (added, removed int) {
	out, err := git(h.dir, "diff", "--numstat", from, to, "--", "*.go", ":(exclude)*_test.go")
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		a, _ := strconv.Atoi(f[0])
		r, _ := strconv.Atoi(f[1])
		added, removed = added+a, removed+r
	}
	return added, removed
}

// Commits is the run's commits, oldest first.
func (h *History) Commits() []string {
	out, err := git(h.dir, "rev-list", "--reverse", h.Start+"..HEAD")
	if err != nil || out == "" {
		return nil
	}
	return strings.Fields(out)
}

// Checkout moves the work tree to a commit of the run.
func (h *History) Checkout(commit string) error {
	_, err := git(h.dir, "checkout", "-q", "--detach", commit)
	return err
}

// Bisect finds the first commit at which bad holds, given that it does not
// hold at Start and does at the last commit. It leaves the tree at the last
// commit.
func (h *History) Bisect(commits []string, bad func() bool) (string, error) {
	last := commits[len(commits)-1]
	lo, hi := -1, len(commits)-1 // commits[lo] good (Start when -1), commits[hi] bad
	for hi-lo > 1 {
		mid := (lo + hi) / 2
		if err := h.Checkout(commits[mid]); err != nil {
			return "", err
		}
		if bad() {
			hi = mid
		} else {
			lo = mid
		}
	}
	return commits[hi], h.Checkout(last)
}

// ResetTo moves the run back to a commit, discarding the ones after it, and
// records why they went.
//
// Replaying the later commits with cherry-pick was the first way: any commit
// whose lines touched the dropped one's conflicted and went with it, however
// independent - four inlines lost for one bad one. Going back and detecting
// again finds the independent changes afresh, on the tree as it now is.
func (h *History) ResetTo(commit, why string) error {
	gone, _ := git(h.dir, "rev-list", commit+"..HEAD")
	if _, err := git(h.dir, "reset", "-q", "--hard", commit); err != nil {
		return err
	}
	for _, c := range strings.Fields(gone) {
		h.db.Exec(`UPDATE changes SET dropped=? WHERE run=? AND commit_hash=?`, why, h.Run, c)
	}
	return nil
}

// KeyOf is the candidate key a commit of the run was made from.
func (h *History) KeyOf(commit string) string {
	body, _ := git(h.dir, "log", "-1", "--format=%b", commit)
	for _, line := range strings.Split(body, "\n") {
		if k, ok := strings.CutPrefix(line, "sx-key: "); ok {
			return k
		}
	}
	return ""
}

// Squash turns the run's commits into one with the summary as its message,
// after keeping them under refs/sx/runs/<run> for inspection.
func (h *History) Squash(summary string) error {
	if len(h.Commits()) == 0 {
		return nil
	}
	head, _ := h.Head()
	if _, err := git(h.dir, "update-ref", "refs/sx/runs/"+h.Run, head); err != nil {
		return err
	}
	if _, err := git(h.dir, "reset", "-q", "--soft", h.Start); err != nil {
		return err
	}
	_, err := git(h.dir, commitArgs(h.dir, "-m", summary)...)
	return err
}

// LOC is the lines added and removed over the whole run.
func (h *History) LOC() (added, removed int) {
	return h.loc(h.Start, "HEAD")
}

// FlakyTests is every test the database has seen both pass and fail on the
// same tree: nothing changed, the outcome did.
func FlakyTests(dir string) Failures {
	db, err := openStore(dir)
	if err != nil {
		return nil
	}
	defer db.Close()
	rows, err := db.Query(`SELECT DISTINCT package, test FROM tests WHERE instr(test, '/') = 0
		GROUP BY tree, package, test
		HAVING SUM(outcome = 'pass') > 0 AND SUM(outcome = 'fail') > 0`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := Failures{}
	for rows.Next() {
		var pkg, test string
		if rows.Scan(&pkg, &test) == nil {
			out[pkg+"\x00"+test] = true
		}
	}
	return out
}

// RunRow is one recorded run, for sx status.
type RunRow struct {
	Run                                           string
	NodesBefore, NodesAfter, LOCAdded, LOCRemoved int
	Attempts, Kept, Dropped                       int
	Dedup, Dead, Inline, Eg                       int
	Duration                                      time.Duration
}

// Runs is every finished run recorded for the repository dir is in, oldest
// first.
func Runs(dir string) ([]RunRow, error) {
	db, err := openStore(dir)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT run, nodes_before, nodes_after, loc_added, loc_removed, attempts, kept, dropped,
		dedup, dead, inline, eg, started_at, finished_at FROM runs WHERE finished_at IS NOT NULL ORDER BY run`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RunRow
	for rows.Next() {
		var r RunRow
		var started, finished string
		if err := rows.Scan(&r.Run, &r.NodesBefore, &r.NodesAfter, &r.LOCAdded, &r.LOCRemoved, &r.Attempts, &r.Kept, &r.Dropped,
			&r.Dedup, &r.Dead, &r.Inline, &r.Eg, &started, &finished); err != nil {
			return nil, err
		}
		s, _ := time.Parse(time.RFC3339, started)
		f, _ := time.Parse(time.RFC3339, finished)
		r.Duration = f.Sub(s)
		out = append(out, r)
	}
	return out, rows.Err()
}

// KindOf is what a commit of the run did, read from its subject: replaying
// commits after a dropped one gives them new hashes, but not new subjects.
func (h *History) KindOf(commit string) Kind {
	subject, _ := git(h.dir, "log", "-1", "--format=%s", commit)
	if f := strings.Fields(subject); len(f) > 1 && f[0] == "sx:" {
		return Kind(f[1])
	}
	return ""
}
