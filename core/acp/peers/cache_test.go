package peers

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/acp"
	"github.com/kameas-ai/kenaz-harness/core/acp/verify"
)

type fakeFetcher struct {
	calls atomic.Int32
	cards []acp.AgentCard // returned in order; last entry repeats
	err   error
}

func (f *fakeFetcher) FetchCard(_ context.Context, peer acp.PeerProfile) (acp.AgentCard, error) {
	idx := int(f.calls.Add(1)) - 1
	if f.err != nil {
		return acp.AgentCard{}, f.err
	}
	if idx >= len(f.cards) {
		idx = len(f.cards) - 1
	}
	c := f.cards[idx]
	c.AgentID = peer.PeerID
	return c, nil
}

func TestCacheHitWithinTTL(t *testing.T) {
	t.Parallel()
	em := &recordingEmitter{}
	now := time.Now()
	clock := func() time.Time { return now }
	f := &fakeFetcher{cards: []acp.AgentCard{
		{Name: "p", Version: "1.0.0", ProtocolVersion: acp.ProtocolVersion},
	}}
	cache := NewCardCache(f, verify.UnsignedAcceptVerifier{}, em, WithClock(clock))
	peer := acp.PeerProfile{PeerID: "p", Transport: acp.TransportLoopback, CardSource: acp.CardSourceWellKnown, CardCacheTTL: 60 * time.Second}

	// First call: fetched.
	if _, err := cache.Get(context.Background(), peer); err != nil {
		t.Fatal(err)
	}
	// Second call within TTL: hit, no extra fetch.
	if _, err := cache.Get(context.Background(), peer); err != nil {
		t.Fatal(err)
	}
	if got := f.calls.Load(); got != 1 {
		t.Errorf("FetchCard called %d times, want 1", got)
	}
	if len(em.cardFetched) != 1 {
		t.Errorf("cardFetched = %v, want 1 entry", em.cardFetched)
	}
	if len(em.cardCacheHit) != 1 {
		t.Errorf("cardCacheHit = %v, want 1 entry", em.cardCacheHit)
	}
}

func TestCacheMissAfterTTL(t *testing.T) {
	t.Parallel()
	em := &recordingEmitter{}
	now := time.Now()
	clock := func() time.Time { return now }
	f := &fakeFetcher{cards: []acp.AgentCard{
		{Name: "p", Version: "1.0.0", ProtocolVersion: acp.ProtocolVersion},
	}}
	cache := NewCardCache(f, verify.UnsignedAcceptVerifier{}, em, WithClock(clock))
	peer := acp.PeerProfile{PeerID: "p", Transport: acp.TransportLoopback, CardSource: acp.CardSourceWellKnown, CardCacheTTL: time.Second}
	if _, err := cache.Get(context.Background(), peer); err != nil {
		t.Fatal(err)
	}
	// Advance clock past TTL.
	now = now.Add(2 * time.Second)
	if _, err := cache.Get(context.Background(), peer); err != nil {
		t.Fatal(err)
	}
	if got := f.calls.Load(); got != 2 {
		t.Errorf("FetchCard called %d times, want 2", got)
	}
	if len(em.cardCacheMissed) != 1 {
		t.Errorf("cardCacheMissed = %v, want 1", em.cardCacheMissed)
	}
}

func TestCacheVersionMismatch(t *testing.T) {
	t.Parallel()
	em := &recordingEmitter{}
	now := time.Now()
	clock := func() time.Time { return now }
	f := &fakeFetcher{cards: []acp.AgentCard{
		{Name: "p", Version: "1.0.0", ProtocolVersion: acp.ProtocolVersion},
		{Name: "p", Version: "1.1.0", ProtocolVersion: acp.ProtocolVersion},
	}}
	cache := NewCardCache(f, verify.UnsignedAcceptVerifier{}, em, WithClock(clock))
	peer := acp.PeerProfile{PeerID: "p", Transport: acp.TransportLoopback, CardSource: acp.CardSourceWellKnown, CardCacheTTL: 50 * time.Millisecond}
	c1, err := cache.Get(context.Background(), peer)
	if err != nil {
		t.Fatal(err)
	}
	if c1.Version != "1.0.0" {
		t.Fatalf("first version = %q, want 1.0.0", c1.Version)
	}
	// Force expiry, then a refetch returning a bumped version.
	now = now.Add(time.Second)
	c2, err := cache.Get(context.Background(), peer)
	if err != nil {
		t.Fatal(err)
	}
	if c2.Version != "1.1.0" {
		t.Errorf("refetched version = %q, want 1.1.0", c2.Version)
	}
	if len(em.cardCacheMissed) != 1 {
		t.Errorf("cache_miss should fire on refetch, got %v", em.cardCacheMissed)
	}
}

func TestCacheVerifierRejection(t *testing.T) {
	t.Parallel()
	em := &recordingEmitter{}
	f := &fakeFetcher{cards: []acp.AgentCard{
		{Name: "p", Version: "1.0.0", ProtocolVersion: acp.ProtocolVersion},
	}}
	cache := NewCardCache(f, verify.RefusalVerifier{Reason: "no trust"}, em)
	peer := acp.PeerProfile{PeerID: "p", Transport: acp.TransportLoopback, CardSource: acp.CardSourceWellKnown}
	_, err := cache.Get(context.Background(), peer)
	if !errors.Is(err, acp.ErrVerifierRejected) {
		t.Fatalf("Get err = %v, want ErrVerifierRejected", err)
	}
	if cache.Size() != 0 {
		t.Errorf("rejected card must not populate cache; size=%d", cache.Size())
	}
}

func TestCacheFetcherError(t *testing.T) {
	t.Parallel()
	em := &recordingEmitter{}
	f := &fakeFetcher{err: errors.New("boom")}
	cache := NewCardCache(f, verify.UnsignedAcceptVerifier{}, em)
	peer := acp.PeerProfile{PeerID: "p", Transport: acp.TransportLoopback, CardSource: acp.CardSourceWellKnown}
	if _, err := cache.Get(context.Background(), peer); err == nil {
		t.Fatal("expected fetcher error to propagate")
	}
}

func TestCacheInvalidate(t *testing.T) {
	t.Parallel()
	em := &recordingEmitter{}
	f := &fakeFetcher{cards: []acp.AgentCard{
		{Name: "p", Version: "1.0.0", ProtocolVersion: acp.ProtocolVersion},
	}}
	cache := NewCardCache(f, verify.UnsignedAcceptVerifier{}, em)
	peer := acp.PeerProfile{PeerID: "p", Transport: acp.TransportLoopback, CardSource: acp.CardSourceWellKnown, CardCacheTTL: time.Hour}
	if _, err := cache.Get(context.Background(), peer); err != nil {
		t.Fatal(err)
	}
	cache.Invalidate("p")
	if cache.Size() != 0 {
		t.Errorf("Invalidate left %d entries", cache.Size())
	}
}
