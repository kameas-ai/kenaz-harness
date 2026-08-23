package registry

// wp14_capability_probe_test.go — model-settings-reach-the-model-01PMZ101
// WP14 AC-017(c) and AC-017(d), plus the end-to-end probe→cache→hints
// pipeline A-5 describes ("probe → cache → CapabilityHints reader").
// AC-017(a) (real sqlite write) and AC-017(b) (CheckAttachments must
// be covered, not just Check) live in core/llm/capabilities — the
// package that owns Gate and SQLiteCache — per spec §8 rule 3 ("assert
// through the real Gate", which for those two is the capabilities
// package's own Gate, not a Registry round-trip).

import (
	"context"
	"sync"
	"testing"
	"time"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/capabilities"
	"github.com/kameas-ai/kenaz-harness/core/llm/credref"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

// capabilityProbeAdapter is a fakeAdapter that also implements
// llm.CapabilitiesProvider, so Registry.Stream's MaybeRefresh trigger
// (registry.go, "1b. Capability cache overlay + background refresh")
// actually fires for it.
type capabilityProbeAdapter struct {
	*fakeAdapter
	probeCaps llm.ProviderCapabilities
	probeErr  error
	probed    chan struct{} // closed on the first ProviderCapabilities call, for test synchronisation
}

func newCapabilityProbeAdapter(kind string) *capabilityProbeAdapter {
	return &capabilityProbeAdapter{
		fakeAdapter: &fakeAdapter{kind: kind, wantCap: llm.CapabilityDescriptor{Supported: map[llm.Capability]bool{llm.CapStreaming: true}}},
		probed:      make(chan struct{}, 8),
	}
}

func (a *capabilityProbeAdapter) ProviderCapabilities(_ context.Context, modelID string) (llm.ProviderCapabilities, error) {
	select {
	case a.probed <- struct{}{}:
	default:
	}
	if a.probeErr != nil {
		return llm.ProviderCapabilities{}, a.probeErr
	}
	caps := a.probeCaps
	caps.Model = modelID
	return caps, nil
}

var _ llm.CapabilitiesProvider = (*capabilityProbeAdapter)(nil)

// endpointCapabilityProbeAdapter extends capabilityProbeAdapter with
// llm.EndpointCapabilitiesProvider (H4 fix), recording the endpoint
// argument each ProviderCapabilitiesAt call received so tests can
// assert refreshCapabilities routed the caller's actual profile
// endpoint through, rather than falling back to whatever the shared
// adapter's own process-wide default happens to be.
type endpointCapabilityProbeAdapter struct {
	*capabilityProbeAdapter
	mu           sync.Mutex
	gotEndpoints []string
}

func (a *endpointCapabilityProbeAdapter) ProviderCapabilitiesAt(_ context.Context, endpoint, modelID string) (llm.ProviderCapabilities, error) {
	a.mu.Lock()
	a.gotEndpoints = append(a.gotEndpoints, endpoint)
	a.mu.Unlock()
	if a.probeErr != nil {
		return llm.ProviderCapabilities{}, a.probeErr
	}
	caps := a.probeCaps
	caps.Model = modelID
	return caps, nil
}

var _ llm.EndpointCapabilitiesProvider = (*endpointCapabilityProbeAdapter)(nil)

// TestRegistry_RefreshCapabilities_UsesProfileEndpoint is the H4
// falsification test for the endpoint-routing half of the finding
// (review finding H4, 2026-08-23). A single adapter instance is shared
// by every profile of its Kind (r.adapters is keyed by Kind, not by
// profile ID), but plain llm.CapabilitiesProvider carries no endpoint
// parameter. refreshCapabilities must route through
// llm.EndpointCapabilitiesProvider with THIS profile's Endpoint when
// the adapter implements it — otherwise a remote profile's capability
// probe silently targets whatever host the adapter's own process-wide
// default happens to be, and the wrong-host answer gets cached under
// the remote profile's ID and fed straight into Gate.Check.
func TestRegistry_RefreshCapabilities_UsesProfileEndpoint(t *testing.T) {
	r, _ := newReg(t)
	adapter := &endpointCapabilityProbeAdapter{capabilityProbeAdapter: newCapabilityProbeAdapter("custom-openai")}
	r.RegisterAdapter(adapter)

	const remoteEndpoint = "https://remote-ollama.example.internal:11434"
	t.Setenv("ZZ_H4_ENDPOINT_TEST_KEY", "secret")
	prof := llm.ProviderProfile{
		ID: "remote-p", Kind: "custom-openai", Model: "m", Endpoint: remoteEndpoint,
		Cred: llm.CredentialReference{Kind: "env", Locator: "ZZ_H4_ENDPOINT_TEST_KEY"},
	}
	if err := r.LoadProfiles([]llm.ProviderProfile{prof}); err != nil {
		t.Fatal(err)
	}

	if _, err := r.refreshCapabilities(context.Background(), "remote-p", "m"); err != nil {
		t.Fatalf("refreshCapabilities: %v", err)
	}

	adapter.mu.Lock()
	got := append([]string(nil), adapter.gotEndpoints...)
	adapter.mu.Unlock()
	if len(got) != 1 || got[0] != remoteEndpoint {
		t.Fatalf("ProviderCapabilitiesAt endpoint = %v, want [%q] — refreshCapabilities must route the profile's own Endpoint, not the adapter's process-wide default", got, remoteEndpoint)
	}
}

// TestRegistry_Stream_CacheHitOverlaysCapabilityHints proves the
// "cache → CapabilityHints reader" half of A-5's merge order directly:
// a pre-populated cache entry for (profileID, model) must be visible
// to Gate.Check on the very next Stream call, even before any
// background refresh has run.
func TestRegistry_Stream_CacheHitOverlaysCapabilityHints(t *testing.T) {
	sink, cache := newRegWithCache(t)
	r := sink.r

	adapter := &fakeAdapter{kind: "custom-openai", wantCap: llm.CapabilityDescriptor{}}
	r.RegisterAdapter(adapter)
	setResolvedCred(t, r)

	prof := llm.ProviderProfile{ID: "p", Kind: "custom-openai", Model: "hosted-model", Endpoint: "http://localhost:1234", Cred: llm.CredentialReference{Kind: "env", Locator: sink.credKey}}
	if err := r.LoadProfiles([]llm.ProviderProfile{prof}); err != nil {
		t.Fatal(err)
	}

	// custom-openai.yaml's static baseline sets reasoning: false — a
	// reasoning-requesting request must be refused with no cache entry.
	req := llm.GenerationRequest{ProfileID: "p", Reasoning: &llm.ReasoningSpec{Enabled: true, BudgetTokens: 512}}
	if _, err := r.Stream(context.Background(), req); err == nil {
		t.Fatal("baseline: expected reasoning rejection with no cache entry")
	}

	// Pre-populate the cache directly (simulating a probe that already
	// completed) with a record advertising Reasoning=true, and assert
	// the SAME request now passes. ProbedCapabilities must name Reasoning
	// explicitly (review finding M5): ProbedSupported() only overlays the
	// keys a producer declares it actually determined, so a record that
	// left this empty would overlay nothing and this assertion would
	// (correctly) fail the baseline-rejection request all over again.
	if err := cache.Put(context.Background(), "p", "hosted-model", llm.ProviderCapabilities{
		Provider: "custom-openai", Model: "hosted-model", Streaming: true, ToolCalling: true, Reasoning: true,
		ProbedCapabilities: []llm.Capability{llm.CapReasoning},
	}); err != nil {
		t.Fatalf("cache.Put: %v", err)
	}
	stream, err := r.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream after cache Put = %v, want nil (cache hit must overlay onto Gate.Check)", err)
	}
	for range stream.Events() {
	}
	if _, err := stream.Final(); err != nil {
		t.Fatalf("Final: %v", err)
	}
}

// TestRegistry_Stream_CacheHitOnlyOverlaysProbedFields is the M5
// falsification test (review finding M5, 2026-08-23). A cache record
// whose producer only verified ToolCalling via a live probe must not
// overlay ANY other field it happens to carry — even though the
// record's own Reasoning/StructuredOutput flags are stored as true
// here (simulating a ProviderCapabilities value that started from a
// dense static-baseline copy, e.g. Catalog.DescribeRich, and only had
// 1 of its 12 Supported-mapped fields actually overwritten by a live
// probe — exactly the shape core/llm/ollama.Adapter.ProviderCapabilities
// produces). Before the fix (overlaying cached.ToDescriptor().Supported
// wholesale instead of cached.ProbedSupported()), BOTH requests below
// would incorrectly pass — the cache hit would shadow every field the
// static catalog baseline correctly said false, for up to cacheTTL,
// contradicting spec A-5's invariant that the probe result merges over
// the baseline for ONLY the capabilities it verified
// (core/llm/capabilities/gate.go:35-37).
func TestRegistry_Stream_CacheHitOnlyOverlaysProbedFields(t *testing.T) {
	sink, cache := newRegWithCache(t)
	r := sink.r

	adapter := &fakeAdapter{kind: "custom-openai", wantCap: llm.CapabilityDescriptor{}}
	r.RegisterAdapter(adapter)
	setResolvedCred(t, r)

	prof := llm.ProviderProfile{ID: "p", Kind: "custom-openai", Model: "hosted-model", Endpoint: "http://localhost:1234", Cred: llm.CredentialReference{Kind: "env", Locator: sink.credKey}}
	if err := r.LoadProfiles([]llm.ProviderProfile{prof}); err != nil {
		t.Fatal(err)
	}

	// Cache record claims Reasoning=true AND StructuredOutput=true, but
	// only declares ToolCalling as actually probed.
	if err := cache.Put(context.Background(), "p", "hosted-model", llm.ProviderCapabilities{
		Provider: "custom-openai", Model: "hosted-model",
		Streaming: true, ToolCalling: true, Reasoning: true, StructuredOutput: true,
		ProbedCapabilities: []llm.Capability{llm.CapToolCalling},
	}); err != nil {
		t.Fatalf("cache.Put: %v", err)
	}

	// Reasoning was NOT declared probed — must still be refused by the
	// static custom-openai.yaml baseline (reasoning: false), even
	// though the cached record's Reasoning field reads true.
	reasoningReq := llm.GenerationRequest{ProfileID: "p", Reasoning: &llm.ReasoningSpec{Enabled: true, BudgetTokens: 512}}
	if _, err := r.Stream(context.Background(), reasoningReq); err == nil {
		t.Fatal("M5: a cache record that only probed ToolCalling must NOT overlay Reasoning, even though the cached record's Reasoning field is true")
	}

	// StructuredOutput was NOT declared probed either — same assertion,
	// a second field to rule out "only Reasoning is special-cased."
	structReq := llm.GenerationRequest{ProfileID: "p", ResponseFormat: &llm.ResponseFormat{Mode: "json_schema"}}
	if _, err := r.Stream(context.Background(), structReq); err == nil {
		t.Fatal("M5: a cache record that only probed ToolCalling must NOT overlay StructuredOutput either")
	}
}

// TestRegistry_Stream_ProbeFailureLeavesBaselineIntact is AC-017(c)
// verbatim: "a probe failure leaves the static baseline intact and
// tool-calling permitted." An adapter whose ProviderCapabilities
// always errors must never cause Stream to fail or degrade a request
// the static catalog already answers — MaybeRefresh treats a refresh
// error as non-fatal and simply leaves no cache entry behind.
func TestRegistry_Stream_ProbeFailureLeavesBaselineIntact(t *testing.T) {
	r, _ := newReg(t)
	adapter := newCapabilityProbeAdapter("custom-openai")
	adapter.probeErr = context.DeadlineExceeded
	r.RegisterAdapter(adapter)
	setResolvedCred(t, r)

	prof := llm.ProviderProfile{ID: "p", Kind: "custom-openai", Model: "m", Endpoint: "http://localhost:1234", Cred: llm.CredentialReference{Kind: "env", Locator: "ZZ_WP14_PROBE_FAIL_KEY"}}
	t.Setenv("ZZ_WP14_PROBE_FAIL_KEY", "secret")
	if err := r.LoadProfiles([]llm.ProviderProfile{prof}); err != nil {
		t.Fatal(err)
	}

	// tool_calling: true is custom-openai.yaml's baseline — this must
	// pass regardless of the (failing) probe.
	req := llm.GenerationRequest{ProfileID: "p", Tools: []llm.ToolSpec{{Name: "x"}}}
	stream, err := r.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream with a failing probe = %v, want nil (static baseline must still decide)", err)
	}
	for range stream.Events() {
	}
	if _, err := stream.Final(); err != nil {
		t.Fatalf("Final: %v", err)
	}

	// Wait for the background refresh goroutine MaybeRefresh started
	// to actually run and fail, then assert nothing landed in cache.
	select {
	case <-adapter.probed:
	case <-time.After(5 * time.Second):
		t.Fatal("ProviderCapabilities was never called — MaybeRefresh did not fire for a CapabilitiesProvider adapter")
	}
	time.Sleep(50 * time.Millisecond)
	if _, ok := r.cache.Get(context.Background(), "p", "m"); ok {
		t.Fatal("a failed probe must not leave a cache entry — Refresher.MaybeRefresh's refresh error path must skip Put")
	}
}

// TestRegistry_Stream_MaybeRefreshPopulatesCacheOnReadPath is AC-017(d):
// "MaybeRefresh is on the read path, not Start." Registry never calls
// Refresher.Start anywhere (grep of registry.go confirms zero call
// sites); this test proves the read-path trigger alone is sufficient
// to eventually populate the cache from a single Stream call, with no
// periodic ticker involved.
func TestRegistry_Stream_MaybeRefreshPopulatesCacheOnReadPath(t *testing.T) {
	r, _ := newReg(t)
	adapter := newCapabilityProbeAdapter("custom-openai")
	adapter.probeCaps = llm.ProviderCapabilities{Streaming: true, ToolCalling: true, Vision: true}
	r.RegisterAdapter(adapter)
	setResolvedCred(t, r)

	prof := llm.ProviderProfile{ID: "p", Kind: "custom-openai", Model: "m2", Endpoint: "http://localhost:1234", Cred: llm.CredentialReference{Kind: "env", Locator: "ZZ_WP14_REFRESH_KEY"}}
	t.Setenv("ZZ_WP14_REFRESH_KEY", "secret")
	if err := r.LoadProfiles([]llm.ProviderProfile{prof}); err != nil {
		t.Fatal(err)
	}

	req := llm.GenerationRequest{ProfileID: "p", Tools: []llm.ToolSpec{{Name: "x"}}}
	stream, err := r.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range stream.Events() {
	}
	_, _ = stream.Final()

	select {
	case <-adapter.probed:
	case <-time.After(5 * time.Second):
		t.Fatal("ProviderCapabilities was never called from a plain Stream call — the read-path trigger did not fire")
	}
	// Give the background goroutine time to Put.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got, ok := r.cache.Get(context.Background(), "p", "m2"); ok {
			if !got.Vision {
				t.Fatalf("cached record = %+v, want Vision=true from the probe", got)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("cache never populated after MaybeRefresh fired")
}

// TestRegistry_Evict_InvalidatesCapabilityCache asserts Evict's new
// cache-invalidation side effect (registry.go doc comment on Evict).
func TestRegistry_Evict_InvalidatesCapabilityCache(t *testing.T) {
	r, _ := newReg(t)
	if err := r.cache.Put(context.Background(), "evict-me", "m", llm.ProviderCapabilities{Streaming: true}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, ok := r.cache.Get(context.Background(), "evict-me", "m"); !ok {
		t.Fatal("setup: expected a cache hit before Evict")
	}
	if err := r.Evict("evict-me"); err != nil {
		t.Fatalf("Evict: %v", err)
	}
	if _, ok := r.cache.Get(context.Background(), "evict-me", "m"); ok {
		t.Fatal("Evict must invalidate the profile's cached capability probe")
	}
}

// ── test helpers ─────────────────────────────────────────────────────────

type regWithCache struct {
	r       *Registry
	credKey string
}

func newRegWithCache(t *testing.T) (regWithCache, capabilities.CapabilityCache) {
	t.Helper()
	cache := capabilities.NewMemoryCache()
	r, err := New(Options{Cache: cache})
	if err != nil {
		t.Fatal(err)
	}
	const key = "ZZ_WP14_CACHE_OVERLAY_KEY"
	t.Setenv(key, "secret")
	return regWithCache{r: r, credKey: key}, cache
}

// setResolvedCred wires a real credref.Resolver, matching the pattern
// used elsewhere in this package (e.g.
// TestRegistry_StructuredOutput_GrammarModeSkipsValidation), so Stream
// doesn't fail at credential resolution before ever reaching the
// capability gate.
func setResolvedCred(t *testing.T, r *Registry) {
	t.Helper()
	r.resolver = credref.New(secrets.NewMemoryBackend())
}
