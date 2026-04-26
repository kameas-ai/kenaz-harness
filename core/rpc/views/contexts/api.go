// Package contexts is the view-scoped accessor for the Context Library.
//
// The package replaces the original rpc/views/contextview stub. Naming:
// the stdlib's `context` is imported under its plain name so this
// package keeps the descriptive name `contexts` (mirrors core/contexts);
// callers alias it on import — see api.go in core/rpc.
//
// Wire shapes are deliberately small — Node, Recent — so the JSON
// payload that crosses the Wails boundary stays compact even for
// libraries with hundreds of files.
package contexts

import (
	"context"
	"time"
)

// NodeKind discriminates files from folders in the tree response.
type NodeKind string

const (
	KindFolder NodeKind = "folder"
	KindFile   NodeKind = "file"
)

// Node is a single tree entry. Path is slash-separated and relative to
// the library root. The frontend never sees an absolute path.
type Node struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	Kind     NodeKind  `json:"kind"`
	Size     int64     `json:"size,omitempty"`
	Modified time.Time `json:"modified,omitempty"`
	Children []Node    `json:"children,omitempty"`
}

// ContextsAPI is the view-scoped surface backing /contexts.
//
// All path arguments are validated against the library root by the
// implementation; bindings reject ".." / absolute paths / symlink
// escape before any I/O.
type ContextsAPI interface {
	// List returns the recursive tree rooted at the library. An
	// empty library returns a Node with Kind=folder and no children.
	// Dotfiles are filtered.
	List(ctx context.Context) (Node, error)
	// ListAll is like List but surfaces dotfile entries (excluding
	// the chassis-internal .trash dir and .recent.json metadata).
	// The /contexts "Show hidden" toggle calls this when on.
	ListAll(ctx context.Context) (Node, error)
	// Get reads a file's contents as a string.
	Get(ctx context.Context, path string) (string, error)
	// Save writes content to path; creates parent directories.
	Save(ctx context.Context, path, content string) error
	// CreateFolder creates a directory (recursively).
	CreateFolder(ctx context.Context, path string) error
	// Rename moves oldPath to newPath. Both validated.
	Rename(ctx context.Context, oldPath, newPath string) error
	// Delete moves path into <root>/.trash/<ts>-<basename>.
	Delete(ctx context.Context, path string) error
	// RecentlyApplied returns up to limit most-recently-applied
	// paths (LRU-ordered, newest first). WP01 ships the surface;
	// later WPs flip the call sites that mark a path applied.
	RecentlyApplied(ctx context.Context, limit int) ([]string, error)
	// RootPath returns the absolute path of the library root. The
	// shell uses this as a hint ("library at <path>") and the
	// "open in finder" affordance.
	RootPath(ctx context.Context) (string, error)
}
