package activities

import (
	"errors"
	"fmt"
	"sync"

	agentgraph "github.com/sigil-tech/kaneaz-harness/core/agentgraph"
)

// ErrNotFound is returned by Resolve / Get when the (id, version) tuple
// has no match in the catalog.
var ErrNotFound = errors.New("activities: not found")

// Catalog is the in-memory registry of every loaded activity. It
// satisfies `agentgraph.ActivityCatalog`, so a populated catalog can
// be passed straight to `agentgraph.Env.Activities`.
//
// Versioning model: the loader keeps every (ID, Version) pair it sees;
// Resolve(id, "") returns the canonical entry preferring an empty
// Version (treated as the "latest unversioned" variant) and falls back
// to the highest-sorted version string if no unversioned entry exists.
// User-supplied entries replace bundled entries on (ID, Version) collision.
type Catalog struct {
	mu sync.RWMutex
	// byID is the activity store, indexed first by activity-id then by
	// version (empty version is the canonical bucket).
	byID map[string]map[string]Entry
}

func newCatalog() *Catalog {
	return &Catalog{byID: map[string]map[string]Entry{}}
}

// NewEmptyCatalog returns a Catalog with no entries. Useful in tests
// when wiring an ActivityCatalog seam without filesystem I/O.
func NewEmptyCatalog() *Catalog { return newCatalog() }

// put records an entry, replacing any prior entry at the same
// (ID, Version) tuple.
func (c *Catalog) put(e Entry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.byID[e.ID] == nil {
		c.byID[e.ID] = map[string]Entry{}
	}
	c.byID[e.ID][e.Version] = e
}

// AddGraph is the test/programmatic entry point: register a fully
// constructed Graph at (id, version). The hash field is derived from
// a marshalled YAML round-trip when source-bytes are not available;
// callers should prefer LoadCatalog when they have on-disk YAML.
func (c *Catalog) AddGraph(g agentgraph.Graph, version string) error {
	if g.ID == "" {
		return errors.New("activities: graph missing id")
	}
	data, err := agentgraph.DumpYAML(g)
	if err != nil {
		return fmt.Errorf("activities: marshal: %w", err)
	}
	ent, err := parseEntry(data, "programmatic")
	if err != nil {
		return err
	}
	if version != "" {
		ent.Version = version
	}
	c.put(ent)
	return nil
}

// Get returns the (ID, Version) entry. Empty version asks for the
// canonical (unversioned) variant; it falls back to the
// lexicographically-largest version when no canonical exists.
func (c *Catalog) Get(id, version string) (Entry, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	versions, ok := c.byID[id]
	if !ok || len(versions) == 0 {
		return Entry{}, fmt.Errorf("%w: id=%q", ErrNotFound, id)
	}
	if version != "" {
		if e, ok := versions[version]; ok {
			return e, nil
		}
		return Entry{}, fmt.Errorf("%w: id=%q version=%q", ErrNotFound, id, version)
	}
	if e, ok := versions[""]; ok {
		return e, nil
	}
	// Choose the highest sorted version when there's no unversioned default.
	var best string
	for v := range versions {
		if v > best {
			best = v
		}
	}
	return versions[best], nil
}

// Resolve satisfies agentgraph.ActivityCatalog. It returns a *Graph
// pointer so the kernel's ActivityNode executor can splice the
// sub-graph into a sub-run. Recursive expansion is the kernel's
// responsibility — when the resolved graph itself contains an
// ActivityNode, the executor calls Resolve again on the nested ref.
//
// The returned pointer aliases the catalog's stored Graph value; the
// kernel must not mutate the spec mid-run. The agentgraph types are
// designed as immutable data carriers, so this is the documented
// contract.
func (c *Catalog) Resolve(activityID, version string) (*agentgraph.Graph, error) {
	e, err := c.Get(activityID, version)
	if err != nil {
		return nil, err
	}
	g := e.Graph
	return &g, nil
}

// Has reports whether the catalog knows about activityID. Used by the
// validator's ActivityRegistry seam (WP01) so unknown references are
// surfaced at validate-time rather than at run-time.
func (c *Catalog) HasActivity(activityID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	versions, ok := c.byID[activityID]
	return ok && len(versions) > 0
}

// List returns every entry sorted by (ID, Version). Stable for UI use.
func (c *Catalog) List() []Entry { return c.listSorted() }

// Versions returns the version tags registered under activityID
// (empty-string entries are returned as "" so callers see the
// unversioned-default variant explicitly).
func (c *Catalog) Versions(activityID string) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	versions := c.byID[activityID]
	out := make([]string, 0, len(versions))
	for v := range versions {
		out = append(out, v)
	}
	return out
}

// Compile-time witness: *Catalog satisfies the kernel-side seam.
var _ agentgraph.ActivityCatalog = (*Catalog)(nil)

// Compile-time witness: *Catalog satisfies the validator's
// ActivityRegistry seam (validator.go).
var _ agentgraph.ActivityRegistry = (*Catalog)(nil)
