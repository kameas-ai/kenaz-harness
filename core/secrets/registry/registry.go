// Package registry implements the dispatch table that routes a
// CredentialReference to its Backend.
//
// The registry is the only object that knows which backends exist at
// runtime. Backends register themselves at startup with Register, and
// the Resolver looks up the backend for a given RefKind via Lookup.
//
// Per DIRECTIVE_001 / C-001, this package imports zero backend SDKs;
// it is the choke point that lets backend subpackages be added without
// modifying any consumer.
//
// Spec mapping: FR-002, FR-017, NFR-005, C-001.
package registry

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/secrets/ref"
	"github.com/sigil-tech/kaneaz-harness/core/secrets/secret"
)

// BackendKind is the canonical name of a backend implementation
// (e.g. "env", "oskeychain", "file"). It is the string consumers see
// in event payloads and `harness secrets status` output.
type BackendKind string

// HealthStatus is the discrete health state a backend reports.
type HealthStatus string

const (
	HealthOK          HealthStatus = "ok"
	HealthDegraded    HealthStatus = "degraded"
	HealthUnavailable HealthStatus = "unavailable"
)

// BackendHealth is the structured result of Backend.Health.
type BackendHealth struct {
	// Status is the discrete state.
	Status HealthStatus
	// Message is a redaction-safe human message. Backends MUST NOT
	// place the credential value (or any locator that could leak one)
	// here.
	Message string
	// LastChecked is when the probe last ran.
	LastChecked time.Time
}

// Backend is the contract every credential resolver implements.
//
// Implementations live in their own subpackage under
// `core/secrets/backends/<name>` and import only the SDK they actually
// need. No other `core/` package may import a backend SDK directly.
type Backend interface {
	// Kind returns the canonical backend name.
	Kind() BackendKind
	// SupportedRefKinds returns the set of ref.RefKind values this
	// backend resolves.
	SupportedRefKinds() []ref.RefKind
	// Resolve produces a Secret for r. On error the returned error
	// MUST wrap one of the sentinels in `core/secrets/errors` so the
	// caller can branch on errors.Is. Until WP07 lands, backends may
	// return any error and the registry will not introspect it.
	Resolve(ctx context.Context, r ref.CredentialReference) (secret.Secret, error)
	// Health returns the backend's current health probe.
	Health(ctx context.Context) BackendHealth
}

// Registry maps RefKind → Backend with safe concurrent registration
// and lookup.
//
// The zero value is unusable; call New().
type Registry struct {
	mu       sync.RWMutex
	byRef    map[ref.RefKind]Backend
	byKind   map[BackendKind]Backend
	allOrder []BackendKind // insertion order for List()
}

// New returns an empty Registry.
func New() *Registry {
	return &Registry{
		byRef:  make(map[ref.RefKind]Backend),
		byKind: make(map[BackendKind]Backend),
	}
}

// Errors returned by registry operations. WP07 will retire these
// in favour of `core/secrets/errors` sentinels.
var (
	// ErrNoBackend is returned by Lookup / Dispatch when no backend
	// is registered for the requested RefKind.
	ErrNoBackend = errors.New("registry: no backend registered for ref kind")
	// ErrDuplicateRefKind is returned by Register when a RefKind has
	// already been claimed by another backend.
	ErrDuplicateRefKind = errors.New("registry: duplicate registration for ref kind")
	// ErrDuplicateBackendKind is returned by Register when the same
	// BackendKind name is reused.
	ErrDuplicateBackendKind = errors.New("registry: duplicate backend kind")
	// ErrBackendNil is returned by Register when called with nil.
	ErrBackendNil = errors.New("registry: nil backend")
	// ErrUnsupportedRefKind is returned when a backend advertises an
	// unsupported / unknown ref kind.
	ErrUnsupportedRefKind = errors.New("registry: backend advertises unsupported ref kind")
)

// Register adds b to the registry. It claims every ref.RefKind that
// b.SupportedRefKinds reports, and the BackendKind name. Duplicate
// claims are typed errors.
//
// Register is safe to call from multiple goroutines.
func (r *Registry) Register(b Backend) error {
	if b == nil {
		return ErrBackendNil
	}
	bk := b.Kind()
	if bk == "" {
		return fmt.Errorf("%w: empty BackendKind", ErrUnsupportedRefKind)
	}
	supported := b.SupportedRefKinds()
	if len(supported) == 0 {
		return fmt.Errorf("%w: backend %q claims no ref kinds", ErrUnsupportedRefKind, bk)
	}
	for _, k := range supported {
		if k == ref.RefUnknown {
			return fmt.Errorf("%w: backend %q claims RefUnknown", ErrUnsupportedRefKind, bk)
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byKind[bk]; ok {
		return fmt.Errorf("%w: %q", ErrDuplicateBackendKind, bk)
	}
	for _, k := range supported {
		if existing, ok := r.byRef[k]; ok {
			return fmt.Errorf("%w: kind=%s already=%s new=%s", ErrDuplicateRefKind, k, existing.Kind(), bk)
		}
	}
	r.byKind[bk] = b
	r.allOrder = append(r.allOrder, bk)
	for _, k := range supported {
		r.byRef[k] = b
	}
	return nil
}

// Lookup returns the backend registered for k, or ErrNoBackend.
func (r *Registry) Lookup(k ref.RefKind) (Backend, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	b, ok := r.byRef[k]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoBackend, k)
	}
	return b, nil
}

// LookupByKind returns the backend registered under name kind, or
// ErrNoBackend.
func (r *Registry) LookupByKind(kind BackendKind) (Backend, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	b, ok := r.byKind[kind]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoBackend, kind)
	}
	return b, nil
}

// List returns backends in registration order.
func (r *Registry) List() []Backend {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Backend, 0, len(r.allOrder))
	for _, k := range r.allOrder {
		out = append(out, r.byKind[k])
	}
	return out
}

// Kinds returns the sorted set of BackendKind names registered.
func (r *Registry) Kinds() []BackendKind {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]BackendKind, 0, len(r.byKind))
	for k := range r.byKind {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Dispatch is the convenience wrapper used by the Resolver: look up
// the backend for r.Kind and call Resolve. It returns
// ErrNoBackend (wrapped) on lookup miss.
func (r *Registry) Dispatch(ctx context.Context, cred ref.CredentialReference) (secret.Secret, Backend, error) {
	b, err := r.Lookup(cred.Kind)
	if err != nil {
		return nil, nil, err
	}
	s, err := b.Resolve(ctx, cred)
	return s, b, err
}
