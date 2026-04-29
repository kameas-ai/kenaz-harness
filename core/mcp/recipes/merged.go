// Package recipes — merged catalog (shipped + registry + user).
//
// MergedCatalog composes up to three recipe sources into a single
// view with id-keyed precedence: user > registry > shipped. Sources
// are pluggable: each is a function that returns a snapshot slice
// (so a hot-reloading user store and an embedded shipped catalog
// can both feed the same merger without a shared interface).
//
// Precedence rule (FR-001 / spec §3 row 1):
//   - For each recipe ID, the highest-priority source that publishes
//     it wins; lower-priority entries with the same ID are silently
//     shadowed (logged at debug level, never elided to the caller —
//     RecipeBySource still returns shadowed entries under their
//     original source so the UI can render "your user copy shadows
//     the shipped 'X' recipe").
//   - Source precedence (high → low): user/imported, registry,
//     shipped. user and imported are sibling sources — both come
//     from UserStore, both shadow registry/shipped on collision.
//     Their relative order is undefined when they collide with each
//     other (a user shouldn't have the same id in both _imports/
//     and the top-level dir; if they do, the loader picks
//     whichever the directory walk hit first).
//
// WP06 wiring: the registry source is a separate constructor slot
// rather than a hard-coded second source, so the upcoming WP06
// curated registry plugs in by passing a non-nil RegistrySource —
// no MergedCatalog re-architecture required.
//
// Spec mapping: FR-001 (layered loader), FR-007 (source-tagged
// rendering in tools view), risk register row "User edits recipe
// in-place via vim" (copy-on-write reload).
package recipes

import "sync"

// SourceFn returns a snapshot slice of recipes. Callers MUST treat
// the returned slice as read-only — but MergedCatalog defensively
// copies anyway so a buggy source can't corrupt other readers.
//
// Each invocation produces the most recent snapshot the source
// knows about; a hot-reloading source (like UserStore) reads its
// internal atomic pointer; a static source (like Shipped()) returns
// the same content each time.
type SourceFn func() []Recipe

// MergedCatalog merges shipped + (optional) registry + user recipe
// sources with id-keyed precedence. The struct is safe for
// concurrent reads.
//
// The merge happens lazily: every Recipes() call rebuilds the merged
// view from the current source snapshots so a UserStore.Reload is
// reflected on the next Recipes() invocation. The cost is O(N) in
// the total recipe count, which is small (< 50 today), so we don't
// cache.
type MergedCatalog struct {
	mu       sync.RWMutex
	shipped  SourceFn
	registry SourceFn
	user     SourceFn
}

// NewMergedCatalog constructs a MergedCatalog from three sources.
// shippedSrc is required; registrySrc and userSrc may be nil.
//
// A nil source contributes zero recipes and never panics — this is
// the WP05 boot path: registry isn't shipped until WP06, so callers
// pass nil for now.
func NewMergedCatalog(shippedSrc, registrySrc, userSrc SourceFn) *MergedCatalog {
	return &MergedCatalog{
		shipped:  shippedSrc,
		registry: registrySrc,
		user:     userSrc,
	}
}

// SetUserSource swaps the user-source function. Useful when the
// boot wiring constructs the catalog before the UserStore is ready,
// then wires the live source once Load returns.
func (m *MergedCatalog) SetUserSource(src SourceFn) {
	m.mu.Lock()
	m.user = src
	m.mu.Unlock()
}

// SetRegistrySource swaps the registry-source function. WP06 calls
// this once to plug in the curated registry without re-constructing
// the merged catalog (the rpc layer holds a pointer to it).
func (m *MergedCatalog) SetRegistrySource(src SourceFn) {
	m.mu.Lock()
	m.registry = src
	m.mu.Unlock()
}

// Recipes returns the merged, deduplicated, source-tagged slice in
// stable order: shipped first (declaration order), then registry
// entries that don't collide with shipped, then user/imported
// entries that don't collide with anything. When a higher-priority
// source shadows a lower one, the higher entry replaces the lower
// in-place (keeping the lower entry's slot in the output order)
// rather than appending — this keeps the UI list stable across a
// user save.
//
// The returned slice is a fresh copy; callers may mutate it freely
// (copy-on-write).
func (m *MergedCatalog) Recipes() []Recipe {
	m.mu.RLock()
	shippedSrc := m.shipped
	registrySrc := m.registry
	userSrc := m.user
	m.mu.RUnlock()

	shipped := callSource(shippedSrc)
	registry := callSource(registrySrc)
	user := callSource(userSrc)

	// Build an id → index map as we go so later (higher-precedence)
	// sources can replace earlier entries in-place. A separate set
	// (idSet) tracks ids we've already placed so a registry entry
	// that doesn't shadow shipped lands at the tail rather than
	// re-using a shipped slot.
	out := make([]Recipe, 0, len(shipped)+len(registry)+len(user))
	idx := make(map[string]int, len(shipped)+len(registry)+len(user))

	for _, r := range shipped {
		if r.Source == "" {
			r.Source = SourceShipped
		}
		if i, ok := idx[r.ID]; ok {
			out[i] = r
			continue
		}
		idx[r.ID] = len(out)
		out = append(out, r)
	}
	for _, r := range registry {
		if r.Source == "" {
			r.Source = SourceRegistry
		}
		if i, ok := idx[r.ID]; ok {
			out[i] = r
			continue
		}
		idx[r.ID] = len(out)
		out = append(out, r)
	}
	for _, r := range user {
		if r.Source == "" {
			r.Source = SourceUser
		}
		if i, ok := idx[r.ID]; ok {
			out[i] = r
			continue
		}
		idx[r.ID] = len(out)
		out = append(out, r)
	}
	return out
}

// Get returns the merged recipe for id (the highest-precedence
// source's copy) or (zero, false) on miss.
func (m *MergedCatalog) Get(id string) (Recipe, bool) {
	for _, r := range m.Recipes() {
		if r.ID == id {
			return r, true
		}
	}
	return Recipe{}, false
}

// RecipeBySource returns the merged recipes whose Source equals
// source. The result is the post-merge view: a recipe ID present in
// both shipped and user with source="user" is reported under "user"
// (not under "shipped"), because the merged catalog has shadowed
// the shipped entry. Callers that need the un-shadowed lower-tier
// entries should walk the source slices directly.
//
// The return is a fresh slice (copy-on-write).
func (m *MergedCatalog) RecipeBySource(source string) []Recipe {
	all := m.Recipes()
	out := make([]Recipe, 0, len(all))
	for _, r := range all {
		if r.Source == source {
			out = append(out, r)
		}
	}
	return out
}

// callSource returns nil when src is nil so callers can branch on
// len() rather than the function pointer. It also defensively copies
// the returned slice so a buggy source that hands back its internal
// slice can't be mutated by MergedCatalog's caller.
func callSource(src SourceFn) []Recipe {
	if src == nil {
		return nil
	}
	raw := src()
	if len(raw) == 0 {
		return nil
	}
	out := make([]Recipe, len(raw))
	copy(out, raw)
	return out
}
