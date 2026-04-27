package corpus

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// defaultExtensions is the v1 whitelist. Code, prose, and config —
// nothing binary. The caller can override via IngestOptions.Extensions.
var defaultExtensions = []string{
	".md", ".markdown", ".mdx", ".txt", ".rst",
	".go", ".py", ".js", ".jsx", ".ts", ".tsx",
	".java", ".kt", ".rb", ".rs", ".c", ".h",
	".cpp", ".hpp", ".cc", ".cs",
	".sh", ".bash", ".zsh",
	".yaml", ".yml", ".toml", ".ini",
	".json",
	".html", ".css", ".scss", ".vue",
	".sql",
}

// walkPaths returns absolute paths under root matching opts. A single
// file argument returns just that file (after extension/size filtering).
// A directory walks recursively when opts.Recursive (default), and
// surface-only when opts.Recursive is explicitly false.
//
// Privacy: directories whose name starts with `.` are skipped (.git,
// .venv, node_modules-by-convention) so we don't ingest secrets.
// node_modules / vendor are skipped explicitly.
func walkPaths(root string, opts IngestOptions) ([]string, error) {
	if root == "" {
		return nil, errors.New("corpus: empty root")
	}
	maxBytes := opts.MaxFileBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxFileBytes
	}
	allowed := opts.Extensions
	if len(allowed) == 0 {
		allowed = defaultExtensions
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, e := range allowed {
		allowedSet[strings.ToLower(strings.TrimSpace(e))] = struct{}{}
	}

	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		if !accept(root, info, allowedSet, maxBytes) {
			return nil, nil
		}
		return []string{root}, nil
	}

	out := make([]string, 0, 32)
	walk := func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Permissions / vanished dir — skip rather than abort the run.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if path == root {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if shouldSkipDir(name) {
				return fs.SkipDir
			}
			if !opts.Recursive {
				// Top-level only.
				return fs.SkipDir
			}
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		if accept(path, fi, allowedSet, maxBytes) {
			out = append(out, path)
		}
		return nil
	}
	if err := filepath.WalkDir(root, walk); err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// accept reports whether a file passes the extension + size filters.
func accept(path string, fi fs.FileInfo, allowed map[string]struct{}, maxBytes int64) bool {
	if fi.Mode()&os.ModeSymlink != 0 {
		// Don't follow symlinks during walk — privacy + cycle safety.
		return false
	}
	if fi.Size() > maxBytes {
		return false
	}
	if fi.Size() == 0 {
		// Allow empty files; manifest row gets written so re-ingest skips them.
		return true
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" {
		return false
	}
	_, ok := allowed[ext]
	return ok
}

// shouldSkipDir returns true for directory names we never want to walk.
// Mirrors the engineering-corpora intent: ignore dotfiles, vendor dirs,
// build artifacts.
func shouldSkipDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "node_modules", "vendor", "dist", "build", "target",
		"__pycache__", "venv", "env", ".venv", ".env":
		return true
	}
	return false
}
