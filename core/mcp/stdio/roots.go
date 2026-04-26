package stdio

import (
	"context"
)

// defaultRoots is the canonical RootsHandler implementation. It
// answers every roots/list request with the harness DataDir plus —
// when projectRoot is non-nil and returns a non-empty path — the
// active project root. Both are wrapped in `file://` URIs.
//
// FR-005 (mission-level): the harness has no current-project concept
// in v1. The pool wires projectRoot as nil today; the project
// mission lights it up later by passing a real selector.
type defaultRoots struct {
	dataDir     string
	projectRoot func() string
}

// DefaultRoots constructs the canonical RootsHandler. dataDir is the
// harness data directory (the "always-on" root). projectRoot is an
// optional selector for the active project root — pass nil to skip
// it. A non-nil selector that returns "" is treated the same as nil
// for that one call.
func DefaultRoots(dataDir string, projectRoot func() string) RootsHandler {
	return &defaultRoots{dataDir: dataDir, projectRoot: projectRoot}
}

// Roots satisfies the RootsHandler interface. The server argument is
// accepted for symmetry with the WP01 interface but ignored — root
// advertisement is currently global, not per-server.
func (d *defaultRoots) Roots(_ context.Context, _ string) ([]Root, error) {
	roots := make([]Root, 0, 2)
	if d.dataDir != "" {
		roots = append(roots, Root{URI: "file://" + d.dataDir, Name: "data"})
	}
	if d.projectRoot != nil {
		if p := d.projectRoot(); p != "" {
			roots = append(roots, Root{URI: "file://" + p, Name: "project"})
		}
	}
	return roots, nil
}
