package envelope

import (
	"io/fs"
	"path/filepath"
	"strings"
)

// walkGo invokes fn for every .go file (including _test.go) under
// root, skipping hidden directories and vendored trees.
func walkGo(root string, fn func(path string) error) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if name == "vendor" || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") {
			return nil
		}
		return fn(path)
	})
}
