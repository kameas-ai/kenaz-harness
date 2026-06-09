package kinds

import (
	"fmt"
	"sort"
	"sync"

	bundle "github.com/kameas-ai/kenaz-harness/core/bundle"
)

// Registry is the kind-handler registry. Implementations MUST be
// concurrency-safe — registration may happen during init() and
// dispatch happens from the resolver's parallel fetch pipeline.
type Registry interface {
	// Register adds a handler. Returns ErrDuplicateKindRegistration if a
	// handler is already registered for handler.Kind().
	Register(h ArtifactKindHandler) error
	// Lookup returns the handler for kind. The boolean is false when no
	// handler is registered.
	Lookup(kind string) (ArtifactKindHandler, bool)
	// List returns the registered kind ids in stable lexical order.
	List() []string
}

// NewRegistry returns an empty in-memory registry.
func NewRegistry() Registry {
	return &mapRegistry{handlers: make(map[string]ArtifactKindHandler)}
}

type mapRegistry struct {
	mu       sync.RWMutex
	handlers map[string]ArtifactKindHandler
}

func (r *mapRegistry) Register(h ArtifactKindHandler) error {
	if h == nil {
		return fmt.Errorf("kinds: nil handler")
	}
	kind := h.Kind()
	if kind == "" {
		return fmt.Errorf("kinds: handler with empty Kind()")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.handlers[kind]; exists {
		return fmt.Errorf("%w: kind=%s", bundle.ErrDuplicateKindRegistration, kind)
	}
	r.handlers[kind] = h
	return nil
}

func (r *mapRegistry) Lookup(kind string) (ArtifactKindHandler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.handlers[kind]
	return h, ok
}

func (r *mapRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.handlers))
	for k := range r.handlers {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
