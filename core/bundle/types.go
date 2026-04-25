package bundle

// Dependency is a bundle-set entry pointing the resolver at a bundle to
// resolve. Stub shape — config and session reference this type today;
// the resolver mission will replace these with manifest-backed types in a
// follow-up integration once consumers have settled on the canonical
// reference format.
type Dependency struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Source  string `json:"source,omitempty"`
}

// Override is a per-resolution override applied on top of a Dependency.
// Stub shape — see Dependency.
type Override struct {
	Name string         `json:"name"`
	Set  map[string]any `json:"set,omitempty"`
}

// Resolver is the resolver façade core.Core depends on. The bundle
// resolver mission's full implementation lives under core/bundle/resolver/
// (separate WP); this empty interface keeps core/core.go compiling until
// the resolver is wired in. Consumers of the real resolver should depend
// on the concrete sub-package interface, not this stub.
type Resolver interface{}
