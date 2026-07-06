package peers

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/acp"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

func TestRegistryLoadAndLookup(t *testing.T) {
	t.Parallel()
	r := NewRegistry(nil, NoopEmitter{})
	profiles := []acp.PeerProfile{
		{PeerID: "a", Transport: acp.TransportLoopback, CardSource: acp.CardSourceWellKnown},
		{PeerID: "b", Transport: acp.TransportUDS, CardSource: acp.CardSourceInline,
			InlineCard: &acp.AgentCard{Name: "b"}},
	}
	if err := r.Load(profiles); err != nil {
		t.Fatalf("Load err = %v", err)
	}
	got, err := r.Lookup("a")
	if err != nil {
		t.Fatalf("Lookup err = %v", err)
	}
	if got.PeerID != "a" {
		t.Errorf("Lookup.PeerID = %q, want a", got.PeerID)
	}
	if _, err := r.Lookup("nope"); !errors.Is(err, acp.ErrPeerNotFound) {
		t.Errorf("Lookup unknown err = %v, want ErrPeerNotFound", err)
	}
	all := r.All()
	if len(all) != 2 || all[0].PeerID != "a" || all[1].PeerID != "b" {
		t.Errorf("All() = %+v, want stable sorted by peer_id", all)
	}
}

func TestRegistryLoadDuplicateRejected(t *testing.T) {
	t.Parallel()
	r := NewRegistry(nil, NoopEmitter{})
	err := r.Load([]acp.PeerProfile{
		{PeerID: "a", Transport: acp.TransportLoopback, CardSource: acp.CardSourceWellKnown},
		{PeerID: "a", Transport: acp.TransportLoopback, CardSource: acp.CardSourceWellKnown},
	})
	if err == nil {
		t.Fatal("expected duplicate peer_id to be rejected")
	}
}

func TestRegistryLoadInvalidTransportRejected(t *testing.T) {
	t.Parallel()
	r := NewRegistry(nil, NoopEmitter{})
	err := r.Load([]acp.PeerProfile{
		{PeerID: "a", Transport: acp.TransportKind("ws"), CardSource: acp.CardSourceWellKnown},
	})
	if err == nil {
		t.Fatal("expected invalid transport to be rejected")
	}
}

func TestRegistryLoadEmptyPeerIDRejected(t *testing.T) {
	t.Parallel()
	r := NewRegistry(nil, NoopEmitter{})
	err := r.Load([]acp.PeerProfile{
		{PeerID: "", Transport: acp.TransportLoopback, CardSource: acp.CardSourceWellKnown},
	})
	if err == nil {
		t.Fatal("expected empty peer_id to be rejected")
	}
}

// recordingEmitter captures emitted events for assertions.
type recordingEmitter struct {
	mu              sync.Mutex
	authAttempted   []string
	authFailed      []string
	cardFetched     []string
	cardCacheHit    []string
	cardCacheMissed []string
}

func (r *recordingEmitter) PeerAuthAttempted(_ context.Context, _, peerID, _ string) {
	r.mu.Lock()
	r.authAttempted = append(r.authAttempted, peerID)
	r.mu.Unlock()
}
func (r *recordingEmitter) PeerAuthFailed(_ context.Context, _, peerID, _, _ string) {
	r.mu.Lock()
	r.authFailed = append(r.authFailed, peerID)
	r.mu.Unlock()
}
func (r *recordingEmitter) CardFetched(_ context.Context, _, peerID, _ string) {
	r.mu.Lock()
	r.cardFetched = append(r.cardFetched, peerID)
	r.mu.Unlock()
}
func (r *recordingEmitter) CardCacheHit(_ context.Context, _, peerID, _ string) {
	r.mu.Lock()
	r.cardCacheHit = append(r.cardCacheHit, peerID)
	r.mu.Unlock()
}
func (r *recordingEmitter) CardCacheMiss(_ context.Context, _, peerID, _, _, _ string) {
	r.mu.Lock()
	r.cardCacheMissed = append(r.cardCacheMissed, peerID)
	r.mu.Unlock()
}

func TestPreflightAllSuccessAndFailure(t *testing.T) {
	t.Parallel()
	backend := newStaticBackend(map[string][]byte{
		"keychain|kenaz/peer-a": []byte("secret-a"),
	})
	em := &recordingEmitter{}
	r := NewRegistry(backend, em)
	profiles := []acp.PeerProfile{
		{
			PeerID: "peer-a", Transport: acp.TransportLoopback, CardSource: acp.CardSourceWellKnown,
			AuthRef: &secrets.Reference{Kind: secrets.RefKeychain, Locator: "kenaz/peer-a"},
		},
		{
			PeerID: "peer-b", Transport: acp.TransportLoopback, CardSource: acp.CardSourceWellKnown,
			AuthRef: &secrets.Reference{Kind: secrets.RefKeychain, Locator: "kenaz/missing"},
		},
		{
			// No AuthRef — preflight reports success.
			PeerID: "peer-c", Transport: acp.TransportLoopback, CardSource: acp.CardSourceWellKnown,
		},
	}
	if err := r.Load(profiles); err != nil {
		t.Fatalf("Load err = %v", err)
	}
	res := r.PreflightAll(context.Background())
	if len(res) != 3 {
		t.Fatalf("PreflightAll len = %d, want 3", len(res))
	}
	results := map[string]acp.PreflightResult{}
	for _, r := range res {
		results[r.PeerID] = r
	}
	if !results["peer-a"].Success {
		t.Errorf("peer-a should resolve")
	}
	if results["peer-b"].Success {
		t.Errorf("peer-b should fail")
	}
	if !results["peer-c"].Success {
		t.Errorf("peer-c should resolve (no auth ref)")
	}

	if len(em.authAttempted) != 3 {
		t.Errorf("authAttempted = %v, want 3 entries", em.authAttempted)
	}
	if len(em.authFailed) != 1 || em.authFailed[0] != "peer-b" {
		t.Errorf("authFailed = %v, want [peer-b]", em.authFailed)
	}
}

func TestPreflightAllNoBackend(t *testing.T) {
	t.Parallel()
	em := &recordingEmitter{}
	r := NewRegistry(nil, em)
	if err := r.Load([]acp.PeerProfile{{
		PeerID: "p", Transport: acp.TransportLoopback, CardSource: acp.CardSourceWellKnown,
		AuthRef: &secrets.Reference{Kind: secrets.RefEnv, Locator: "PEER_KEY"},
	}}); err != nil {
		t.Fatal(err)
	}
	res := r.PreflightAll(context.Background())
	if len(res) != 1 || res[0].Success {
		t.Errorf("expected failure with no backend, got %+v", res)
	}
}
