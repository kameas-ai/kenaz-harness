package fs

import (
	"path/filepath"
	"sync"
)

// recipeDirCache is the package-level cache of recipe-declared
// allowed directories. It is populated by NotifyRecipeDirs when
// recipes are installed or removed, and read by RecipeDirs on every
// filesystem gate evaluation. Concurrent access is safe: the value is
// swapped atomically under mu.
var recipeDirCache struct {
	mu   sync.RWMutex
	dirs []string
}

// RecipeDirs returns the cached union of all recipe-declared
// "allowed_directories" values from currently-installed recipes. The
// returned slice is a snapshot: callers may read it without holding a
// lock, but MUST NOT mutate it.
//
// The slice is empty until NotifyRecipeDirs is called for the first
// time (typically at recipe-install time or harness boot). An empty
// result means no recipe-dir read grants are in effect; the cedar
// default policy will produce NotApplicable for reads, and the prompt
// flow will take over.
//
// All entries are absolute, clean paths (filepath.Clean applied during
// population). Trailing slashes are stripped so callers can append
// their own separator when constructing cedar `like` patterns.
func RecipeDirs() []string {
	recipeDirCache.mu.RLock()
	out := recipeDirCache.dirs
	recipeDirCache.mu.RUnlock()
	return out
}

// NotifyRecipeDirs replaces the cached recipe-dir set. It is called by
// the recipe-install and recipe-uninstall paths (see the RecipeDirs
// helper in core/mcp/recipes/recipes.go). dirs is the full union of
// allowed_directories from all currently-enabled recipes; callers are
// responsible for computing the union before calling this function.
//
// Each element is cleaned with filepath.Clean before storage. Entries
// that are empty or non-absolute after cleaning are silently skipped.
//
// Safe for concurrent use: the internal pointer is replaced under a
// write lock in one atomic operation so concurrent RecipeDirs() readers
// always see a consistent snapshot.
func NotifyRecipeDirs(dirs []string) {
	cleaned := make([]string, 0, len(dirs))
	for _, d := range dirs {
		c := filepath.Clean(d)
		if c == "" || c == "." {
			continue
		}
		if !filepath.IsAbs(c) {
			continue
		}
		cleaned = append(cleaned, c)
	}
	recipeDirCache.mu.Lock()
	recipeDirCache.dirs = cleaned
	recipeDirCache.mu.Unlock()
}

// IsInsideRecipeDir reports whether canonical (a Canonicalize-d path)
// falls within any directory in the current RecipeDirs set. It
// performs prefix-matching: a path is "inside" a recipe dir when its
// canonical form starts with the recipe-dir string followed by a
// separator (or equals the recipe-dir exactly).
//
// This is the same membership test the cedar engine runs via the
// recipe_dir_match context attribute; it is also exposed here so the
// gate can populate that attribute before calling Evaluate.
func IsInsideRecipeDir(canonical string) bool {
	dirs := RecipeDirs()
	sep := string(filepath.Separator)
	for _, d := range dirs {
		if canonical == d {
			return true
		}
		if len(canonical) > len(d) && canonical[:len(d)+1] == d+sep {
			return true
		}
	}
	return false
}
