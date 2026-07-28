package fallback_test

// runner_registry_test.go exercises fallback.Runner wrapping a *real*
// core/llm/registry.Registry as its Streamer (model-request-path-live-
// 01PMDL01 WP07). connector_integration_test.go (core/llm package) already
// covers the abstract Runner ↔ Streamer contract against synthetic fakes;
// this file additionally proves the registry itself satisfies Streamer and
// that fallback.StoreResolver correctly turns a per-request FallbackChainId
// (surfaced here via fallback.WithChainIDOverride, standing in for the
// node-attr value a graph-aware call site would thread through) into a
// chain walk against real registered profiles.

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/credref"
	"github.com/kameas-ai/kenaz-harness/core/llm/events"
	"github.com/kameas-ai/kenaz-harness/core/llm/fallback"
	"github.com/kameas-ai/kenaz-harness/core/llm/registry"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

// regFakeAdapter is a minimal llm.ProviderAdapter: always fails with a 503
// (ErrTransient) when failAlways is true, otherwise always succeeds.
type regFakeAdapter struct {
	kind       string
	failAlways bool
	calls      int32
}

func (a *regFakeAdapter) Kind() string { return a.kind }
func (a *regFakeAdapter) Capabilities(_ string) llm.CapabilityDescriptor {
	return llm.CapabilityDescriptor{Provider: a.kind}
}
func (a *regFakeAdapter) Stream(_ context.Context, _ llm.GenerationRequest, _ llm.ProviderProfile, _ []byte) (llm.Stream, error) {
	atomic.AddInt32(&a.calls, 1)
	if a.failAlways {
		return nil, &llm.ErrTransient{Status: 503, Message: "upstream overloaded"}
	}
	return &regFakeStream{}, nil
}

type regFakeStream struct{}

func (s *regFakeStream) Events() <-chan llm.StreamEvent {
	ch := make(chan llm.StreamEvent)
	close(ch)
	return ch
}
func (s *regFakeStream) Cancel() error { return nil }
func (s *regFakeStream) Final() (llm.Response, error) {
	return llm.Response{FinishReason: "stop"}, nil
}

func newTestRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	const key = "TEST_FALLBACK_REG_KEY"
	os.Setenv(key, "secret-bytes")
	t.Cleanup(func() { os.Unsetenv(key) })

	sink := &events.MemorySink{}
	emit := events.New(sink)
	r, err := registry.New(registry.Options{Emitter: emit, Resolver: credref.New(secrets.NewMemoryBackend())})
	if err != nil {
		t.Fatalf("registry.New: %v", err)
	}
	return r
}

func loadProfile(t *testing.T, r *registry.Registry, id, kind, model string) {
	t.Helper()
	if err := r.LoadProfiles([]llm.ProviderProfile{{
		ID: id, Kind: kind, Model: model,
		Cred: llm.CredentialReference{Kind: "env", Locator: "TEST_FALLBACK_REG_KEY"},
		// MaxAttempts: 1 disables the registry's OWN internal transient-error
		// retry middleware for this test — it is orthogonal to (and would
		// otherwise multiply-count against) the fallback.Runner's chain-walk
		// attempts this test is asserting on.
		Retry: &llm.RetryPolicy{MaxAttempts: 1, BaseMS: 1, MaxMS: 1, Jitter: "full"},
	}}); err != nil {
		t.Fatalf("LoadProfiles(%s): %v", id, err)
	}
}

// TestRunner_RealRegistry_FallsThroughToNextModelInChain verifies that a
// fallback.Runner wrapping a real *registry.Registry walks to the next
// chain entry's (provider, model) when the primary profile's adapter call
// fails with a 5xx, and that the caller ultimately observes the fallback
// hop's successful response.
func TestRunner_RealRegistry_FallsThroughToNextModelInChain(t *testing.T) {
	r := newTestRegistry(t)

	primaryAdapter := &regFakeAdapter{kind: "flaky", failAlways: true}
	fallbackAdapter := &regFakeAdapter{kind: "steady", failAlways: false}
	r.RegisterAdapter(primaryAdapter)
	r.RegisterAdapter(fallbackAdapter)

	loadProfile(t, r, "primary", "flaky", "flaky-model-1")
	loadProfile(t, r, "fallback-profile", "steady", "steady-model-1")

	chain := &fallback.Chain{
		ID:   "chain1",
		Name: "primary-then-steady",
		Entries: []fallback.ChainEntry{
			{ProviderID: "fallback-profile", Triggers: []fallback.TriggerCondition{fallback.TriggerError5xx}},
		},
	}
	runner := fallback.NewRunner(r, resolverWithStaticChain{chain})

	req := llm.GenerationRequest{ProfileID: "primary", SessionID: "sess-1"}
	stream, err := runner.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Runner.Stream: unexpected error: %v", err)
	}
	resp, ferr := stream.Final()
	if ferr != nil {
		t.Fatalf("Final: unexpected error: %v", ferr)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("want fallback hop's response surfaced, got %+v", resp)
	}
	if n := atomic.LoadInt32(&primaryAdapter.calls); n != 1 {
		t.Errorf("want exactly 1 primary attempt, got %d", n)
	}
	if n := atomic.LoadInt32(&fallbackAdapter.calls); n != 1 {
		t.Errorf("want exactly 1 fallback-hop attempt, got %d", n)
	}
}

// TestRunner_RealRegistry_MaxWholeChainAttemptsRespected verifies the
// global MaxWholeChainAttempts ceiling caps the walk even when every entry
// in a longer chain matches and fails, and that the typed
// ErrFallbackExhausted is surfaced with the correct attempt count.
func TestRunner_RealRegistry_MaxWholeChainAttemptsRespected(t *testing.T) {
	r := newTestRegistry(t)

	adapter := &regFakeAdapter{kind: "flaky", failAlways: true}
	r.RegisterAdapter(adapter)
	loadProfile(t, r, "primary", "flaky", "m0")

	// Build a chain with MORE entries than the global ceiling, all
	// perpetually failing and all matching TriggerErrorAny.
	entries := make([]fallback.ChainEntry, 0, fallback.MaxWholeChainAttempts+2)
	for i := 0; i < fallback.MaxWholeChainAttempts+2; i++ {
		entries = append(entries, fallback.ChainEntry{
			ProviderID: "primary", // reissues against the same always-failing profile
			Triggers:   []fallback.TriggerCondition{fallback.TriggerErrorAny},
		})
	}
	chain := &fallback.Chain{ID: "long-chain", Name: "long-chain", Entries: entries}
	runner := fallback.NewRunner(r, resolverWithStaticChain{chain})

	req := llm.GenerationRequest{ProfileID: "primary", SessionID: "sess-1"}
	_, err := runner.Stream(context.Background(), req)
	if err == nil {
		t.Fatal("expected ErrFallbackExhausted, got nil")
	}
	var exhausted *fallback.ErrFallbackExhausted
	if !errors.As(err, &exhausted) {
		t.Fatalf("expected *fallback.ErrFallbackExhausted, got %T: %v", err, err)
	}
	if exhausted.Attempts != fallback.MaxWholeChainAttempts {
		t.Errorf("want Attempts == %d (ceiling), got %d", fallback.MaxWholeChainAttempts, exhausted.Attempts)
	}
	// 1 primary call + MaxWholeChainAttempts hop calls.
	if n := atomic.LoadInt32(&adapter.calls); n != int32(1+fallback.MaxWholeChainAttempts) {
		t.Errorf("want %d total adapter calls (1 primary + %d hops), got %d",
			1+fallback.MaxWholeChainAttempts, fallback.MaxWholeChainAttempts, n)
	}
}

// resolverWithStaticChain is a trivial fallback.Resolver that always
// returns the same chain regardless of session, standing in for the
// node-attr-level FallbackChainId a graph-aware call site would supply
// (StoreResolver + WithChainIDOverride cover that resolution path; see
// resolver_test.go for the hierarchy's own unit tests).
type resolverWithStaticChain struct{ chain *fallback.Chain }

func (r resolverWithStaticChain) Resolve(_ context.Context, _ string) (*fallback.Chain, error) {
	return r.chain, nil
}
