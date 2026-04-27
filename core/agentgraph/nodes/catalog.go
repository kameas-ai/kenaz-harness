package nodes

import (
	"fmt"
	"sort"
	"sync"
)

// Catalog is the in-memory registry of every loaded ResolvedManifest.
// Concurrent reads are safe; writes happen only at LoadCatalog time
// (and at WP07's `Nodes_ReloadCatalog` doctor RPC) under the mutex.
//
// The Catalog satisfies the (future) `agentgraph.ManifestCatalog` seam
// declared in `core/agentgraph/seams.go`. WP01 ships the implementation;
// the seam itself is added in WP01-T8 once the parent package needs it.
type Catalog struct {
	mu sync.RWMutex
	// byID indexes resolved manifests by their canonical ID.
	byID map[string]*ResolvedManifest
}

func newCatalog() *Catalog {
	return &Catalog{byID: map[string]*ResolvedManifest{}}
}

// NewEmptyCatalog returns a Catalog with no entries. Useful in tests
// when wiring the seam without filesystem I/O.
func NewEmptyCatalog() *Catalog { return newCatalog() }

// put records a resolved manifest. Replaces any prior entry with the
// same ID (the loader has already enforced uniqueness for shipped
// manifests; user overrides flow through the deep-merge path before
// reaching here).
func (c *Catalog) put(rm *ResolvedManifest) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.byID[rm.Manifest.ID] = rm
}

// Get returns the resolved manifest for the given ID. Returns
// ErrUnknownKind when the catalog has no entry for that ID. Callers
// MAY use errors.Is(err, ErrUnknownKind) to discriminate.
func (c *Catalog) Get(id string) (*ResolvedManifest, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	rm, ok := c.byID[id]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownKind, id)
	}
	return rm, nil
}

// List returns every resolved manifest sorted by ID. Stable ordering
// for UI listings + tests.
func (c *Catalog) List() []*ResolvedManifest {
	return c.listSorted()
}

// ListByCategory returns the resolved manifests whose `category:` (after
// inheritance resolution) matches the requested category. Sort order
// matches List().
func (c *Catalog) ListByCategory(cat Category) []*ResolvedManifest {
	all := c.List()
	out := make([]*ResolvedManifest, 0, len(all))
	for _, rm := range all {
		if rm.Manifest.Category == cat {
			out = append(out, rm)
		}
	}
	return out
}

// ListByArchetype returns the resolved manifests whose inheritance
// chain includes the given archetype ID. Useful for the frontend
// palette tree (Category → Archetype → Kind).
func (c *Catalog) ListByArchetype(archetype string) []*ResolvedManifest {
	all := c.List()
	out := make([]*ResolvedManifest, 0, len(all))
	for _, rm := range all {
		// Skip the archetype itself; only return concrete children.
		if rm.Manifest.ID == archetype {
			continue
		}
		for _, ancestor := range rm.Chain {
			if ancestor == archetype {
				out = append(out, rm)
				break
			}
		}
	}
	return out
}

// Archetypes returns every archetype-layer manifest in the catalog.
// Sorted by ID.
func (c *Catalog) Archetypes() []*ResolvedManifest {
	all := c.List()
	out := make([]*ResolvedManifest, 0, len(all))
	for _, rm := range all {
		if rm.Manifest.IsArchetype() {
			out = append(out, rm)
		}
	}
	return out
}

// Kinds returns every concrete (callable) kind in the catalog. Sorted
// by ID.
func (c *Catalog) Kinds() []*ResolvedManifest {
	all := c.List()
	out := make([]*ResolvedManifest, 0, len(all))
	for _, rm := range all {
		if rm.Manifest.IsCallable() {
			out = append(out, rm)
		}
	}
	return out
}

// IsCallable reports whether the manifest with the given ID is callable
// in v1. Returns false for unknown IDs and for archetype manifests
// (FR-007). Used by the future validator (WP03) to fire the
// archetype-not-callable rule (FR-016).
func (c *Catalog) IsCallable(id string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	rm, ok := c.byID[id]
	if !ok {
		return false
	}
	return rm.Manifest.IsCallable()
}

// IDs returns every catalog ID in sorted order.
func (c *Catalog) IDs() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, 0, len(c.byID))
	for id := range c.byID {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Len returns the number of resolved manifests in the catalog.
func (c *Catalog) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.byID)
}
