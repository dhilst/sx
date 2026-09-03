package cache

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	gofront "purgatrix/internal/frontend/golang"
	"purgatrix/internal/sx"
)

type Cache struct {
	Dir string
}

func DefaultDir(start string) string {
	if git := findGitDir(start); git != "" {
		return filepath.Join(git, "sx-cache")
	}
	return filepath.Join(start, ".sx-cache")
}

func New(dir string) Cache {
	return Cache{Dir: dir}
}

func (c Cache) CompileGoFile(path, rootDir string) (*sx.Graph, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	rel, err := filepath.Rel(rootDir, path)
	if err != nil {
		rel = path
	}
	rel = filepath.ToSlash(rel)
	key := Key(rel, data)
	cachePath := filepath.Join(c.Dir, key[:2], key+".sx")
	if f, err := os.Open(cachePath); err == nil {
		defer f.Close()
		g, err := sx.Decode(f)
		if err == nil && sx.Validate(g) == nil {
			return g, true, nil
		}
	}
	g, err := gofront.CompileFile(path, rootDir)
	if err != nil {
		return nil, false, err
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		return nil, false, err
	}
	var buf bytes.Buffer
	if err := sx.Encode(&buf, g); err != nil {
		return nil, false, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(cachePath), ".tmp-*.sx")
	if err != nil {
		return nil, false, err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return nil, false, err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return nil, false, err
	}
	if err := os.Rename(tmpName, cachePath); err != nil {
		os.Remove(tmpName)
		return nil, false, err
	}
	return g, false, nil
}

func Key(sourceUnitID string, source []byte) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00", sx.SchemaVersion, sx.ModelVersion, gofront.FrontendID, gofront.FrontendVersion, gofront.TranslationSpecVersion)))
	h := sha256.New()
	h.Write(sum[:])
	h.Write([]byte(sourceUnitID))
	h.Write([]byte{0})
	h.Write(source)
	return hex.EncodeToString(h.Sum(nil))
}

func findGitDir(start string) string {
	dir, err := filepath.Abs(start)
	if err != nil {
		return ""
	}
	for {
		git := filepath.Join(dir, ".git")
		if st, err := os.Stat(git); err == nil && st.IsDir() {
			return git
		}
		next := filepath.Dir(dir)
		if next == dir {
			return ""
		}
		dir = next
	}
}
