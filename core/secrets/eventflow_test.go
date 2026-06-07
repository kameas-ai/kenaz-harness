package secrets_test

import (
	"context"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/secrets"
	"github.com/kameas-ai/kenaz-harness/core/secrets/cache"
	"github.com/kameas-ai/kenaz-harness/core/secrets/events"
	"github.com/kameas-ai/kenaz-harness/core/secrets/ref"
	"github.com/kameas-ai/kenaz-harness/core/secrets/registry"
)

// captureLogger is a deterministic EventLogger that records every
// (kind, payload) tuple in arrival order. It is the smallest concrete
// EventLogger that lets us assert which kinds the Resolver emits during
// a Resolve call without depending on the BufferingLogger's drain
// semantics (which discards kinds).
type captureLogger struct {
	kinds    []events.EventKind
	payloads []events.ResolutionEvent
}

func (c *captureLogger) Append(_ context.Context, kind events.EventKind, payload events.ResolutionEvent) error {
	c.kinds = append(c.kinds, kind)
	c.payloads = append(c.payloads, payload)
	return nil
}

// TestResolver_EventFlow_ResolveCacheMissAndHit asserts the canonical
// resolution event sequence flows into a logger:
//
//   - First Resolve (cache miss): requested → cache_miss → backend_dispatched → ok
//   - Second Resolve (cache hit): requested → cache_hit
//
// This is the SEAM 3 contract: when callers wire a real EventLogger
// into secrets.NewResolver, every branch of the resolve pipeline emits
// a structured event. Operators rely on this to audit which credentials
// were touched, by which consumer, with what outcome (FR-012).
func TestResolver_EventFlow_ResolveCacheMissAndHit(t *testing.T) {
	b := &fakeBackend{
		kind:     "fake",
		supports: []ref.RefKind{ref.RefEnv},
		value:    map[string][]byte{"X": []byte("v")},
		health:   registry.HealthOK,
	}
	reg := registry.New()
	if err := reg.Register(b); err != nil {
		t.Fatal(err)
	}
	cap := &captureLogger{}
	r, err := secrets.NewResolver(secrets.ResolverConfig{
		Registry: reg,
		Cache:    cache.New(),
		Logger:   cap,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })

	cred := ref.CredentialReference{Kind: ref.RefEnv, Locator: "X"}

	// First call — cache miss path.
	if _, err := r.Resolve(context.Background(), cred, "consumer-a"); err != nil {
		t.Fatalf("Resolve#1: %v", err)
	}
	wantMiss := []events.EventKind{
		events.KindResolutionRequested,
		events.KindResolutionCacheMiss,
		events.KindResolutionBackendDispatch,
		events.KindResolutionOK,
	}
	if !equalKinds(cap.kinds, wantMiss) {
		t.Fatalf("cache-miss kinds = %v, want %v", cap.kinds, wantMiss)
	}

	// Second call — cache hit path.
	cap.kinds = nil
	cap.payloads = nil
	if _, err := r.Resolve(context.Background(), cred, "consumer-a"); err != nil {
		t.Fatalf("Resolve#2: %v", err)
	}
	wantHit := []events.EventKind{
		events.KindResolutionRequested,
		events.KindResolutionCacheHit,
	}
	if !equalKinds(cap.kinds, wantHit) {
		t.Fatalf("cache-hit kinds = %v, want %v", cap.kinds, wantHit)
	}

	// Sanity-check payload metadata: each event carries the consumer id
	// and the redaction-safe reference id (never the locator).
	for _, p := range cap.payloads {
		if p.ConsumerID != "consumer-a" {
			t.Errorf("event ConsumerID=%q want consumer-a", p.ConsumerID)
		}
		if p.ReferenceID == "" {
			t.Errorf("event ReferenceID empty")
		}
		if p.ReferenceKind != ref.RefEnv.String() {
			t.Errorf("event ReferenceKind=%q want %q", p.ReferenceKind, ref.RefEnv.String())
		}
	}
}

// TestResolver_EventFlow_NoLoggerDropsSilently is the negative
// counterpart: when no Logger is wired, emit must drop without panic.
// Operators booting the harness before event-log is online rely on
// this safety property.
func TestResolver_EventFlow_NoLoggerDropsSilently(t *testing.T) {
	b := &fakeBackend{
		kind:     "fake",
		supports: []ref.RefKind{ref.RefEnv},
		value:    map[string][]byte{"X": []byte("v")},
		health:   registry.HealthOK,
	}
	reg := registry.New()
	if err := reg.Register(b); err != nil {
		t.Fatal(err)
	}
	r, err := secrets.NewResolver(secrets.ResolverConfig{
		Registry: reg,
		Logger:   nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })

	if _, err := r.Resolve(context.Background(),
		ref.CredentialReference{Kind: ref.RefEnv, Locator: "X"}, "consumer-x"); err != nil {
		t.Fatalf("Resolve with nil Logger: %v", err)
	}
}

func equalKinds(a, b []events.EventKind) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
