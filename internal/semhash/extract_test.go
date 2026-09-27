package semhash

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/dhilst/sx/internal/extract"
)

// TestExtractCases proves each of internal/extract's test programs against
// the program with its marked range extracted.
func TestExtractCases(t *testing.T) {
	if testing.Short() {
		t.Skip("extracts and type-checks 46 programs")
	}
	dirs, _ := filepath.Glob("../extract/testdata/cases/*")
	if len(dirs) == 0 {
		t.Fatal("no cases")
	}
	got := map[string]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	proved := 0
	for _, dir := range dirs {
		name := filepath.Base(dir)
		wg.Add(1)
		go func() {
			defer wg.Done()
			src, _ := os.ReadFile(filepath.Join(dir, "main.go"))
			a, b := 0, 0
			for i, l := range strings.Split(string(src), "\n") {
				if strings.Contains(l, "//<") {
					a = i + 1
				}
				if strings.Contains(l, "//>") {
					b = i + 1
				}
			}
			work := t.TempDir()
			os.WriteFile(filepath.Join(work, "go.mod"), []byte("module c\n\ngo 1.25\n"), 0o644)
			os.WriteFile(filepath.Join(work, "main.go"), src, 0o644)
			res, err := extract.Extract(work, "main.go", a, b, extract.Options{})
			v := "extract refused: " + errString(err)
			if err == nil {
				v = Prove(module(t, string(src)), module(t, string(res.Source)), Options{}).String()
			}
			mu.Lock()
			got[name] = v
			if strings.HasPrefix(v, "proved") {
				proved++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	t.Logf("%d/%d extractions proved", proved, len(dirs))
	golden(t, "extract.golden", got)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
