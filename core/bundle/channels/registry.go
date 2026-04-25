package channels

import (
	"fmt"
	"sort"
	"sync"

	bundle "github.com/sigil-tech/kaneaz-harness/core/bundle"
	"github.com/sigil-tech/kaneaz-harness/core/secrets"
)

// Registry maps channel kind ids to factory functions. It is the only
// seam new channel implementations cross to plug into the resolver.
type Registry interface {
	Register(kind string, factory Factory) error
	Open(spec ChannelSpec, creds secrets.Resolver) (Channel, error)
	Kinds() []string
}

// NewRegistry returns an empty in-memory channel registry.
func NewRegistry() Registry {
	return &mapRegistry{factories: make(map[string]Factory)}
}

type mapRegistry struct {
	mu        sync.RWMutex
	factories map[string]Factory
}

func (r *mapRegistry) Register(kind string, factory Factory) error {
	if kind == "" {
		return fmt.Errorf("channels: empty kind")
	}
	if factory == nil {
		return fmt.Errorf("channels: nil factory")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.factories[kind]; exists {
		return fmt.Errorf("channels: kind %q already registered", kind)
	}
	r.factories[kind] = factory
	return nil
}

func (r *mapRegistry) Open(spec ChannelSpec, creds secrets.Resolver) (Channel, error) {
	r.mu.RLock()
	factory, ok := r.factories[spec.Kind]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: kind=%s", bundle.ErrChannelUnknown, spec.Kind)
	}
	return factory(spec, creds)
}

func (r *mapRegistry) Kinds() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.factories))
	for k := range r.factories {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
