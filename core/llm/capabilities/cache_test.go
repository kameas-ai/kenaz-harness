package capabilities_test

import (
	"context"
	"testing"
	"time"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/capabilities"
)

func TestMemoryCache_GetPut(t *testing.T) {
	cache := capabilities.NewMemoryCache()
	ctx := context.Background()

	caps := llm.ProviderCapabilities{
		Provider:  "openai",
		Model:     "gpt-4o",
		Streaming: true,
	}

	// Miss before put.
	if _, ok := cache.Get(ctx, "prof1", "gpt-4o"); ok {
		t.Error("expected cache miss before Put")
	}

	if err := cache.Put(ctx, "prof1", "gpt-4o", caps); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, ok := cache.Get(ctx, "prof1", "gpt-4o")
	if !ok {
		t.Fatal("expected cache hit after Put")
	}
	if got.Provider != "openai" {
		t.Errorf("got provider %q, want openai", got.Provider)
	}
}

func TestMemoryCache_Invalidate(t *testing.T) {
	cache := capabilities.NewMemoryCache()
	ctx := context.Background()

	caps := llm.ProviderCapabilities{Provider: "openai", Model: "gpt-4o"}
	_ = cache.Put(ctx, "prof1", "gpt-4o", caps)
	_ = cache.Put(ctx, "prof1", "gpt-4-turbo", caps)
	_ = cache.Put(ctx, "prof2", "gpt-4o", caps)

	if err := cache.Invalidate(ctx, "prof1"); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}

	if _, ok := cache.Get(ctx, "prof1", "gpt-4o"); ok {
		t.Error("prof1/gpt-4o should be invalidated")
	}
	if _, ok := cache.Get(ctx, "prof1", "gpt-4-turbo"); ok {
		t.Error("prof1/gpt-4-turbo should be invalidated")
	}
	if _, ok := cache.Get(ctx, "prof2", "gpt-4o"); !ok {
		t.Error("prof2/gpt-4o should survive invalidation of prof1")
	}
}

func TestNullCache_AlwaysMisses(t *testing.T) {
	cache := capabilities.NullCache{}
	ctx := context.Background()
	caps := llm.ProviderCapabilities{Provider: "openai", Model: "gpt-4o"}

	_ = cache.Put(ctx, "p", "m", caps)
	if _, ok := cache.Get(ctx, "p", "m"); ok {
		t.Error("NullCache should always miss")
	}
}

func TestRefresher_MaybeRefresh(t *testing.T) {
	cache := capabilities.NewMemoryCache()
	ctx := context.Background()

	refreshCalled := make(chan struct{}, 1)
	refresher := capabilities.NewRefresher(cache, func(_ context.Context, profileID, modelID string) (llm.ProviderCapabilities, error) {
		refreshCalled <- struct{}{}
		return llm.ProviderCapabilities{
			Provider:  "openai",
			Model:     modelID,
			Streaming: true,
		}, nil
	})

	// First call should trigger a refresh (cache miss).
	refresher.MaybeRefresh(ctx, "prof1", "gpt-4o")

	// Wait for the background goroutine to complete.
	select {
	case <-refreshCalled:
	case <-time.After(5 * time.Second):
		t.Fatal("refresh not called within 5s")
	}

	// After refresh, the entry should be in cache.
	// Wait a bit for Put to complete.
	time.Sleep(50 * time.Millisecond)
	got, ok := cache.Get(ctx, "prof1", "gpt-4o")
	if !ok {
		t.Fatal("expected cache hit after refresh")
	}
	if !got.Streaming {
		t.Error("expected Streaming=true from refreshed entry")
	}
}

// TestRefresher_MaybeRefresh_SkipsFreshEntry is the H4 falsification
// test (review finding H4, 2026-08-23). MaybeRefresh's own doc says it
// "checks whether the entry ... needs a background refresh (cache miss
// or stale)" — before the fix it never consulted cache.Get at all and
// spawned a probe goroutine (gated only by the in-flight dedup map) on
// EVERY call, including one for an entry that was already fresh.
// registry.Registry.Stream calls MaybeRefresh on every Stream for a
// profile whose adapter implements CapabilitiesProvider (registry.go's
// "1b." block), so this bug fired a live network probe on every chat
// turn rather than only when an entry was missing or stale.
func TestRefresher_MaybeRefresh_SkipsFreshEntry(t *testing.T) {
	cache := capabilities.NewMemoryCache()
	ctx := context.Background()

	// Pre-populate a FRESH entry directly — no refresh call involved.
	if err := cache.Put(ctx, "prof1", "gpt-4o", llm.ProviderCapabilities{Provider: "openai", Model: "gpt-4o", Streaming: true}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	refreshCalled := make(chan struct{}, 1)
	refresher := capabilities.NewRefresher(cache, func(_ context.Context, _, modelID string) (llm.ProviderCapabilities, error) {
		select {
		case refreshCalled <- struct{}{}:
		default:
		}
		return llm.ProviderCapabilities{Provider: "openai", Model: modelID, Streaming: true}, nil
	})

	refresher.MaybeRefresh(ctx, "prof1", "gpt-4o")

	select {
	case <-refreshCalled:
		t.Fatal("H4: MaybeRefresh spawned a refresh for an entry that was already fresh — it must consult cache.Get before spawning")
	case <-time.After(200 * time.Millisecond):
		// No refresh fired within the window — correct.
	}
}
