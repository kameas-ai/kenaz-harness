// Package peers materialises the bundle-declared A2A peer set
// (FR-004, FR-018) and owns the AgentCard cache (FR-008).
//
// The registry is populated by the WP03 bundle artifact-kind handler
// from `a2a_peer` artifacts; the cache fetches a peer's card on first
// contact via the envelope, validates it via the FR-020 verifier
// seam, and serves subsequent requests from memory until the per-peer
// TTL expires or a version mismatch invalidates the entry.
package peers

import (
	"context"
	"errors"
	"sort"
	"sync"

	"github.com/kameas-ai/kenaz-harness/core/acp"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

// Registry is the in-memory peer_id -> PeerProfile map. It is
// thread-safe: bundle reload can call Load while the client is
// dispatching.
type Registry struct {
	mu        sync.RWMutex
	profiles  map[string]acp.PeerProfile
	secrets   secrets.Backend
	emitter   AuthEventEmitter
}

// AuthEventEmitter is the (small, focused) chunk of the WP12 events
// surface this package needs. Defined here rather than imported from
// core/acp/events to avoid a peers <-> events <-> peers cycle. The
// real events package satisfies this interface.
type AuthEventEmitter interface {
	PeerAuthAttempted(ctx context.Context, sessionID, peerID, refKind string)
	PeerAuthFailed(ctx context.Context, sessionID, peerID, refKind, reason string)
	CardFetched(ctx context.Context, sessionID, peerID, version string)
	CardCacheHit(ctx context.Context, sessionID, peerID, version string)
	CardCacheMiss(ctx context.Context, sessionID, peerID, fromVersion, toVersion, reason string)
}

// NoopEmitter is a Default for tests / contexts where events are not
// connected. The real wiring comes from core/acp/events.
type NoopEmitter struct{}

func (NoopEmitter) PeerAuthAttempted(context.Context, string, string, string)      {}
func (NoopEmitter) PeerAuthFailed(context.Context, string, string, string, string) {}
func (NoopEmitter) CardFetched(context.Context, string, string, string)            {}
func (NoopEmitter) CardCacheHit(context.Context, string, string, string)           {}
func (NoopEmitter) CardCacheMiss(context.Context, string, string, string, string, string) {
}

// NewRegistry constructs a Registry backed by the given secrets
// backend. The emitter is required; pass NoopEmitter{} in tests that
// do not assert audit traffic.
func NewRegistry(sec secrets.Backend, emitter AuthEventEmitter) *Registry {
	if emitter == nil {
		emitter = NoopEmitter{}
	}
	return &Registry{
		profiles: map[string]acp.PeerProfile{},
		secrets:  sec,
		emitter:  emitter,
	}
}

// Load populates the registry from the activation output of the
// WP03 bundle artifact-kind handler. Re-calling Load replaces the
// current peer set wholesale, mirroring bundle reload semantics.
//
// Returns an error if profiles contains a duplicate peer_id or an
// invalid transport (defence-in-depth; the bundle handler should
// reject these earlier).
func (r *Registry) Load(profiles []acp.PeerProfile) error {
	next := make(map[string]acp.PeerProfile, len(profiles))
	for _, p := range profiles {
		if p.PeerID == "" {
			return errors.New("peers: empty peer_id in profile")
		}
		if !p.Transport.Valid() {
			return errors.New("peers: invalid transport for " + p.PeerID)
		}
		if _, exists := next[p.PeerID]; exists {
			return errors.New("peers: duplicate peer_id " + p.PeerID)
		}
		next[p.PeerID] = p
	}
	r.mu.Lock()
	r.profiles = next
	r.mu.Unlock()
	return nil
}

// Lookup returns the PeerProfile for peerID or ErrPeerNotFound.
func (r *Registry) Lookup(peerID string) (acp.PeerProfile, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.profiles[peerID]
	if !ok {
		return acp.PeerProfile{}, acp.ErrPeerNotFound
	}
	return p, nil
}

// All returns a slice of every loaded profile, sorted by peer_id for
// stable iteration in tests and in audit emission.
func (r *Registry) All() []acp.PeerProfile {
	r.mu.RLock()
	out := make([]acp.PeerProfile, 0, len(r.profiles))
	for _, p := range r.profiles {
		out = append(out, p)
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].PeerID < out[j].PeerID })
	return out
}

// PreflightAll resolves every peer's auth_ref against the secrets
// backend and emits one peer_auth_attempted event per peer plus
// peer_auth_failed events for any unresolvable references. Failures
// do NOT mutate the registry; the resolver decides whether to block
// bundle activation. NFR-007.
func (r *Registry) PreflightAll(ctx context.Context) []acp.PreflightResult {
	r.mu.RLock()
	profiles := make([]acp.PeerProfile, 0, len(r.profiles))
	for _, p := range r.profiles {
		profiles = append(profiles, p)
	}
	r.mu.RUnlock()
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].PeerID < profiles[j].PeerID })

	out := make([]acp.PreflightResult, 0, len(profiles))
	for _, p := range profiles {
		if p.AuthRef == nil {
			r.emitter.PeerAuthAttempted(ctx, "", p.PeerID, "")
			out = append(out, acp.PreflightResult{PeerID: p.PeerID, Success: true})
			continue
		}
		refKind := p.AuthRef.Kind.String()
		r.emitter.PeerAuthAttempted(ctx, "", p.PeerID, refKind)
		if r.secrets == nil {
			err := acp.ErrCredentialResolution
			r.emitter.PeerAuthFailed(ctx, "", p.PeerID, refKind, "no secrets backend configured")
			out = append(out, acp.PreflightResult{PeerID: p.PeerID, Success: false, Error: err})
			continue
		}
		s, err := r.secrets.Resolve(ctx, *p.AuthRef)
		if err != nil {
			r.emitter.PeerAuthFailed(ctx, "", p.PeerID, refKind, err.Error())
			out = append(out, acp.PreflightResult{PeerID: p.PeerID, Success: false, Error: err})
			continue
		}
		s.Destroy()
		out = append(out, acp.PreflightResult{PeerID: p.PeerID, Success: true})
	}
	return out
}

// Emitter returns the wired emitter; useful for the cache to share
// the same audit pipeline.
func (r *Registry) Emitter() AuthEventEmitter { return r.emitter }
