// Package mcp's impl provides a concrete MCPAPI that returns the
// configured server list (currently empty — the mcp-client mission
// hasn't landed yet). The structure is right so the view lights up
// the moment servers register through bundles.
//
// The Server entries returned here are reference-only metadata —
// transport descriptors, capability advertisements, and human-readable
// state strings. No tool-call payloads or credentials traverse this
// surface (privacy CI invariant: rpc never re-emits raw payloads).
package mcp

import (
	"context"
	"sort"
	"sync"
)

// Subscriber is the broker contract used by API.StartStream. Mirrors
// the audit-impl decoupling so this package keeps DIRECTIVE_001
// isolation from core/rpc.
type Subscriber interface {
	Subscribe(ctx context.Context, view, kind string, source <-chan any) (string, error)
	Unsubscribe(id string) error
}

// Registry is the seam the mcp-client mission will populate when it
// lands. Today every implementation returns an empty list; the rpc
// layer doesn't care which Registry is wired.
type Registry interface {
	List(ctx context.Context) ([]Server, error)
}

// API is the concrete MCPAPI implementation.
type API struct {
	mu       sync.RWMutex
	registry Registry
	broker   Subscriber
	subs     map[string]chan any
}

// Option configures NewAPI.
type Option func(*API)

// WithRegistry plugs in the mcp-client registry. nil leaves the
// impl returning an empty list — the v1 expected behaviour until
// the client mission ships.
func WithRegistry(r Registry) Option {
	return func(a *API) { a.registry = r }
}

// WithSubscriber injects a streamBroker.
func WithSubscriber(s Subscriber) Option {
	return func(a *API) { a.broker = s }
}

// NewAPI constructs the mcp view-scoped API.
func NewAPI(opts ...Option) *API {
	a := &API{subs: make(map[string]chan any)}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// ListServers returns every configured MCP server, sorted by name.
// Empty when no registry is wired — that's the expected v1 state and
// the frontend renders its empty-state copy.
func (a *API) ListServers(ctx context.Context) ([]Server, error) {
	a.mu.RLock()
	r := a.registry
	a.mu.RUnlock()

	if r == nil {
		return []Server{}, nil
	}
	got, err := r.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Server, len(got))
	copy(out, got)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// StartStream allocates a per-subscription channel for the named
// server's `mcp:event` topic. With no registry wired today the
// channel is created but no events flow until a registry pushes
// through Publish.
func (a *API) StartStream(ctx context.Context, _ string) (string, error) {
	if a.broker == nil {
		return "", nil
	}
	ch := make(chan any, 64)
	id, err := a.broker.Subscribe(ctx, "mcp", "event", ch)
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	a.subs[id] = ch
	a.mu.Unlock()
	return id, nil
}

// StopStream tears down the subscription and releases broker state.
func (a *API) StopStream(_ context.Context, id string) error {
	if a.broker == nil {
		return nil
	}
	a.mu.Lock()
	ch, ok := a.subs[id]
	delete(a.subs, id)
	a.mu.Unlock()
	if ok {
		close(ch)
	}
	return a.broker.Unsubscribe(id)
}

// Publish fans a server event into every active subscriber. Exposed
// for the mcp-client mission to push events without coupling to
// core/rpc directly.
func (a *API) Publish(ev any) {
	a.mu.RLock()
	subs := make([]chan any, 0, len(a.subs))
	for _, ch := range a.subs {
		subs = append(subs, ch)
	}
	a.mu.RUnlock()
	for _, ch := range subs {
		select {
		case ch <- ev:
		default:
			// Drop rather than block — the canonical record is the
			// mcp-client's own log; this surface is best-effort fan-out.
		}
	}
}
