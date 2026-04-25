package engine_test

import (
	"os"
	"path/filepath"
)

// readDir returns the names of entries in dir, or an empty slice on
// error. Test-helper for the import-boundary scan in engine_test.go.
func readDir(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out, nil
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	if err != nil {
		return false
	}
	return info.IsDir()
}

func readFile(p string) (string, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// silence unused-import warning when filepath is not needed in some
// builds. (kept here because future test helpers may expand this.)
var _ = filepath.Join
