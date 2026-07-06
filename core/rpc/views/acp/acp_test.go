// Package acp tests the ACPAPI view layer.
//
// These tests cover the real API (not NullAPI) with a concrete Cedar engine
// loaded from the embedded default ACP policy so the Cedar gate is actually
// invoked. This proves FR-007/FR-008: acp_send to unverified peers is denied;
// acp_send to verified peers is allowed.
package acp

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/acp"
	cedar "github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	cedarlib "github.com/cedar-policy/cedar-go"
)

// ── fakes ────────────────────────────────────────────────────────────────

// fakeRegistry implements RegistryIface for tests.
type fakeRegistry struct {
	mu       sync.Mutex
	profiles []acp.PeerProfile
}

func (r *fakeRegistry) All() []acp.PeerProfile {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]acp.PeerProfile, len(r.profiles))
	copy(out, r.profiles)
	return out
}

func (r *fakeRegistry) Lookup(id string) (acp.PeerProfile, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.profiles {
		if p.PeerID == id {
			return p, nil
		}
	}
	return acp.PeerProfile{}, acp.ErrPeerNotFound
}

func (r *fakeRegistry) Load(ps []acp.PeerProfile) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.profiles = ps
	return nil
}

// fakeEnvelope implements EnvelopeIface for tests. It records dispatched
// payloads and returns a predictable a2aTaskID.
type fakeEnvelope struct {
	mu        sync.Mutex
	dispatched []json.RawMessage
}

func (e *fakeEnvelope) Dispatch(
	_ context.Context,
	_ acp.PeerProfile,
	_ string,
	body json.RawMessage,
) (string, <-chan acp.Message, error) {
	e.mu.Lock()
	e.dispatched = append(e.dispatched, body)
	e.mu.Unlock()
	ch := make(chan acp.Message)
	close(ch)
	return "task-1", ch, nil
}

func (e *fakeEnvelope) FetchCard(_ context.Context, _ acp.PeerProfile) (acp.AgentCard, error) {
	return acp.AgentCard{AgentID: "test-agent", Name: "Test"}, nil
}

func (e *fakeEnvelope) snapshot() []json.RawMessage {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]json.RawMessage, len(e.dispatched))
	copy(out, e.dispatched)
	return out
}

// ── cedar engine adapter for tests ──────────────────────────────────────

// newTestEngine builds an in-memory Cedar engine loaded with the embedded
// ACP default policy. Tests that use this engine exercise the real Cedar
// evaluation path (FR-007, FR-008).
func newTestEngine(t *testing.T) *EngineAdapter {
	t.Helper()
	eng, err := cedar.NewEngine(cedar.Options{
		IncludeEmbedded: true,
		LoadFromDisk:    false,
	})
	if err != nil {
		t.Fatalf("failed to build test cedar engine: %v", err)
	}
	return NewEngineAdapter(eng)
}

// ── helpers ──────────────────────────────────────────────────────────────

// apiWithPeer builds a real API backed by fake infra, adds one peer at
// the given trust tier, and returns the API along with the peer ID.
func apiWithPeer(t *testing.T, eng CedarEngine, trustTier string) (*API, string) {
	t.Helper()
	reg := &fakeRegistry{}
	env := &fakeEnvelope{}
	api := NewAPI(reg, env, Options{Cedar: eng})

	// Add a peer directly to the internal store at the requested trust tier.
	profile := acp.PeerProfile{
		PeerID:      "test-peer-001",
		EndpointURL: "http://localhost:8080",
		Transport:   acp.TransportLoopback,
	}
	card := acp.AgentCard{AgentID: "test-peer-001", Name: "Test Peer"}
	api.peerStor.peers[profile.PeerID] = profile
	api.peerStor.meta[profile.PeerID] = peerMeta{
		trustTier:       trustTier,
		cardFingerprint: cardFingerprint(card),
	}
	return api, profile.PeerID
}

// ── tests ─────────────────────────────────────────────────────────────────

// TestACPDispatch_CedarGate proves FR-007: acp_send is permitted only to
// peers at trust tier "verified"; pending/revoked peers are denied.
func TestACPDispatch_CedarGate(t *testing.T) {
	eng := newTestEngine(t)

	tests := []struct {
		name      string
		tier      string
		wantErr   bool
		errSubstr string
	}{
		{
			name:    "verified peer is allowed",
			tier:    "verified",
			wantErr: false,
		},
		{
			name:      "pending peer is denied",
			tier:      "pending",
			wantErr:   true,
			errSubstr: "policy denied",
		},
		{
			name:      "revoked peer is denied",
			tier:      "revoked",
			wantErr:   true,
			errSubstr: "policy denied",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			api, peerID := apiWithPeer(t, eng, tc.tier)
			ctx := context.Background()

			result, err := api.ACP_Dispatch(ctx, peerID, `{"msg":"hello"}`)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for tier=%q, got nil", tc.tier)
				}
				if tc.errSubstr != "" {
					if !containsSubstr(err.Error(), tc.errSubstr) {
						t.Errorf("error %q does not contain %q", err.Error(), tc.errSubstr)
					}
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error for tier=%q: %v", tc.tier, err)
				}
				if result.EnvelopeID == "" {
					t.Error("expected non-empty EnvelopeID on success")
				}
			}
		})
	}
}

// TestACPDispatch_NilCedar verifies that when no Cedar engine is wired
// (nil), dispatch succeeds for any peer (permissive path).
func TestACPDispatch_NilCedar(t *testing.T) {
	api, peerID := apiWithPeer(t, nil, "pending")
	ctx := context.Background()
	result, err := api.ACP_Dispatch(ctx, peerID, `{"msg":"hello"}`)
	if err != nil {
		t.Fatalf("expected success with nil Cedar engine, got: %v", err)
	}
	if result.EnvelopeID == "" {
		t.Error("expected non-empty EnvelopeID")
	}
}

// TestACPTrustPeer_CardRejection verifies that ACP_TrustPeer rejects a
// card with a missing agent_id (card-rejection path).
func TestACPTrustPeer_CardRejection(t *testing.T) {
	reg := &fakeRegistry{}
	env := &fakeEnvelope{}
	api := NewAPI(reg, env, Options{})

	// Card with empty agent_id — must be rejected.
	badCard := `{"name":"test","version":"1.0"}`
	_, err := api.ACP_TrustPeer(context.Background(), badCard)
	if err == nil {
		t.Fatal("expected error for card with missing agent_id, got nil")
	}
}

// TestACPListTraces_EmptyAtFirst verifies ACP_ListTraces returns an empty
// slice (not an error) when no traces exist for a peer.
func TestACPListTraces_EmptyAtFirst(t *testing.T) {
	api, peerID := apiWithPeer(t, nil, "verified")
	traces, err := api.ACP_ListTraces(context.Background(), peerID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(traces) != 0 {
		t.Errorf("expected 0 traces, got %d", len(traces))
	}
}

// TestACPListTraces_AfterDispatch verifies ACP_ListTraces returns the
// dispatched trace after a successful ACP_Dispatch.
func TestACPListTraces_AfterDispatch(t *testing.T) {
	api, peerID := apiWithPeer(t, nil, "verified")
	ctx := context.Background()

	result, err := api.ACP_Dispatch(ctx, peerID, `{"msg":"hello"}`)
	if err != nil {
		t.Fatalf("dispatch failed: %v", err)
	}

	traces, err := api.ACP_ListTraces(ctx, peerID)
	if err != nil {
		t.Fatalf("list traces failed: %v", err)
	}
	if len(traces) != 1 {
		t.Fatalf("expected 1 trace, got %d", len(traces))
	}
	if traces[0].EnvelopeID != result.EnvelopeID {
		t.Errorf("trace envelope_id %q != dispatch result %q", traces[0].EnvelopeID, result.EnvelopeID)
	}
	if traces[0].PeerID != peerID {
		t.Errorf("trace peer_id %q != %q", traces[0].PeerID, peerID)
	}
	if traces[0].Direction != "send" {
		t.Errorf("expected direction=send, got %q", traces[0].Direction)
	}
}

// TestNewEnvelopeID_Uniqueness verifies that rapid successive calls to
// newEnvelopeID produce distinct identifiers (crypto/rand, no collision
// from wall-clock aliasing).
func TestNewEnvelopeID_Uniqueness(t *testing.T) {
	seen := make(map[string]bool, 100)
	for i := 0; i < 100; i++ {
		id := newEnvelopeID()
		if seen[id] {
			t.Fatalf("duplicate envelope ID: %s", id)
		}
		seen[id] = true
	}
}

// TestInferTransport covers the extended loopback cases added in the review.
func TestInferTransport(t *testing.T) {
	tests := []struct {
		url  string
		want acp.TransportKind
	}{
		{"unix:///tmp/agent.sock", acp.TransportUDS},
		{"http://127.0.0.1:8080/v1", acp.TransportLoopback},
		{"http://localhost:9090/", acp.TransportLoopback},
		{"https://127.0.0.1/api", acp.TransportLoopback},
		{"https://localhost/api", acp.TransportLoopback},
		{"http://[::1]:8080/v1", acp.TransportLoopback},
		{"https://[::1]/api", acp.TransportLoopback},
		{"http://192.168.1.5:8080/", acp.TransportLAN},
		{"https://agent.example.com/", acp.TransportLAN},
	}
	for _, tc := range tests {
		got := inferTransport(tc.url)
		if got != tc.want {
			t.Errorf("inferTransport(%q) = %q, want %q", tc.url, got, tc.want)
		}
	}
}

// TestEngineAdapter_VerifiedAllowed uses the real cedar engine (with the
// embedded ACP policy) to prove that a verified peer gets Allow from
// EngineAdapter.Check.
func TestEngineAdapter_VerifiedAllowed(t *testing.T) {
	eng := newTestEngine(t)
	ctx := context.Background()
	outcome, _, err := eng.Check(ctx, cedar.PrincipalLocal, cedar.ActionACPSend, "peer-verified",
		map[string]interface{}{
			"peer_id":         "peer-verified",
			"peer_trust_tier": "verified",
			"transport":       "http_loopback",
			"direction":       "send",
		},
	)
	if err != nil {
		t.Fatalf("Check error: %v", err)
	}
	if outcome != cedar.Allow {
		t.Errorf("expected Allow for verified peer, got %v", outcome)
	}
}

// TestEngineAdapter_PendingDenied uses the real cedar engine to prove that
// a pending (unverified) peer gets Deny from EngineAdapter.Check.
func TestEngineAdapter_PendingDenied(t *testing.T) {
	eng := newTestEngine(t)
	ctx := context.Background()
	outcome, _, err := eng.Check(ctx, cedar.PrincipalLocal, cedar.ActionACPSend, "peer-pending",
		map[string]interface{}{
			"peer_id":         "peer-pending",
			"peer_trust_tier": "pending",
			"transport":       "http_loopback",
			"direction":       "send",
		},
	)
	if err != nil {
		t.Fatalf("Check error: %v", err)
	}
	if outcome != cedar.Deny {
		t.Errorf("expected Deny for pending peer, got %v", outcome)
	}
}

// ensure cedarlib is referenced (for the adapter's type conversions).
var _ cedarlib.Value = cedarlib.String("")

// ── helpers ─────────────────────────────────────────────────────────────

func containsSubstr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub ||
		len(sub) == 0 ||
		findSubstr(s, sub))
}

func findSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
