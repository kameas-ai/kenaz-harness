package backends

import (
	"sync"

	"kaneaz-harness/core/trust"
)

// Registry is the process-local map of [trust.BackendKind] →
// [SigningBackend].
//
// The dispatcher (core/trust/sign.go) resolves IdentityRef.Backend to a
// registered backend via [Registry.Lookup]. WP01 ships the registry; each
// concrete backend registers itself on init() inside its own subpackage.
type Registry struct {
	mu       sync.RWMutex
	backends map[trust.BackendKind]SigningBackend
}

// NewRegistry constructs an empty backend registry.
func NewRegistry() *Registry {
	return &Registry{backends: make(map[trust.BackendKind]SigningBackend)}
}

// Register adds (or replaces) a backend keyed by kind. It is safe to call
// from init() functions in backend subpackages because the underlying map
// is not shared across registries.
func (r *Registry) Register(b SigningBackend) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.backends[b.Kind()] = b
}

// Lookup resolves a backend by kind.
//
// Returns [trust.ErrBackendNotRegistered] when no backend has been
// registered for the kind — the dispatcher converts this to a typed
// error for the caller.
//
// The return type is [trust.Backend] (a structural alias of
// [SigningBackend] declared in core/trust/) so a *Registry directly
// satisfies [trust.BackendDispatcher]. This avoids a generated adapter
// and keeps the import cycle clean: backends imports trust, and trust
// holds the cross-package interface.
func (r *Registry) Lookup(kind trust.BackendKind) (trust.Backend, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	b, ok := r.backends[kind]
	if !ok {
		return nil, trust.ErrBackendNotRegistered
	}
	return b, nil
}

// Kinds returns every registered BackendKind in stable order. Used by
// preflight to walk the configured backends.
func (r *Registry) Kinds() []trust.BackendKind {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]trust.BackendKind, 0, len(r.backends))
	for k := range r.backends {
		out = append(out, k)
	}
	return out
}
