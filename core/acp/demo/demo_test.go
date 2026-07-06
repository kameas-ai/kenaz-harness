// Package demo exercises the full ACP round-trip from the ACPAPI view
// layer through the in-process peer fixture provided by
// core/acp/envelope.RegisterInProcessPeer.
//
// This test validates the end-to-end integration of:
//   - ACP_TrustPeer: JSON card parse + Verifier + peerStore trust
//   - ACP_Dispatch: Cedar gate (nil/permissive) + in-process envelope
//     dispatch + traceStore recording + audit emit
//   - ACP_GetTrace: trace retrieval by envelope_id
//   - ACP_ListPeers: runtime peer list after trust
//   - ACP_RevokePeer: peer removal
//   - Audit fields: peer_id, transport, outcome, bytes_out must be non-zero
//
// The envelope uses its in-process back-end (no real UDS/network), so
// the test passes under -race -short on all platforms.
package demo

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/acp"
	"github.com/kameas-ai/kenaz-harness/core/acp/envelope"
	coreaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
	acpview "github.com/kameas-ai/kenaz-harness/core/rpc/views/acp"
)

// ── fake audit emitter (race-safe) ────────────────────────────────────────

type emitRecord struct {
	event coreaudit.Event
}

type fakeAuditEmitter struct {
	mu      sync.Mutex
	emitted []emitRecord
}

func (f *fakeAuditEmitter) Emit(_ context.Context, e coreaudit.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.emitted = append(f.emitted, emitRecord{event: e})
	return nil
}

func (f *fakeAuditEmitter) snapshot() []emitRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]emitRecord, len(f.emitted))
	copy(out, f.emitted)
	return out
}

// ── fake registry ─────────────────────────────────────────────────────────

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

func (r *fakeRegistry) Lookup(peerID string) (acp.PeerProfile, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.profiles {
		if p.PeerID == peerID {
			return p, nil
		}
	}
	return acp.PeerProfile{}, errors.New("demo: peer not found: " + peerID)
}

func (r *fakeRegistry) Load(profiles []acp.PeerProfile) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.profiles = profiles
	return nil
}

// ── test ──────────────────────────────────────────────────────────────────

// TestRoundTrip exercises the full ACP send path using the in-process
// envelope fixture. It is the canonical integration smoke test for
// acp-orchestration-integration-01NDFSEX06.
func TestRoundTrip(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Build components.
	em := &fakeAuditEmitter{}
	reg := &fakeRegistry{}
	env := envelope.New()

	api := acpview.NewAPI(reg, env, acpview.Options{
		Audit: em,
	})

	// Step 1: trust the peer via a minimal AgentCard JSON blob.
	card := acp.AgentCard{
		AgentID:         "demo-peer-001",
		Name:            "Demo Peer",
		Description:     "In-process test peer",
		Version:         "1",
		ProtocolVersion: acp.ProtocolVersion,
		EndpointURL:     "unix:///tmp/demo-peer.sock",
		Signed:          false,
	}
	cardJSON, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("marshal card: %v", err)
	}

	trustRes, err := api.ACP_TrustPeer(ctx, string(cardJSON))
	if err != nil {
		t.Fatalf("ACP_TrustPeer: %v", err)
	}
	if trustRes.PeerID != "demo-peer-001" {
		t.Errorf("TrustResult.PeerID = %q, want demo-peer-001", trustRes.PeerID)
	}
	if trustRes.TrustTier != "verified" {
		t.Errorf("TrustResult.TrustTier = %q, want verified", trustRes.TrustTier)
	}

	// Step 2: wire the in-process handler on the envelope.
	// The handler echoes the payload as its response.
	env.RegisterInProcessPeer("demo-peer-001", card, func(_ context.Context, in envelope.AcceptedTask) (json.RawMessage, error) {
		resp := map[string]string{"echo": string(in.Body)}
		return json.Marshal(resp)
	})

	// Step 3: dispatch an envelope to the in-process peer.
	payload := `{"turn":"hello from harness"}`
	dispatchRes, err := api.ACP_Dispatch(ctx, "demo-peer-001", payload)
	if err != nil {
		t.Fatalf("ACP_Dispatch: %v", err)
	}
	if dispatchRes.EnvelopeID == "" {
		t.Error("DispatchResult.EnvelopeID must not be empty")
	}

	// Step 4: retrieve the trace and assert fields.
	trace, err := api.ACP_GetTrace(ctx, dispatchRes.EnvelopeID)
	if err != nil {
		t.Fatalf("ACP_GetTrace: %v", err)
	}
	if trace.EnvelopeID != dispatchRes.EnvelopeID {
		t.Errorf("trace.EnvelopeID = %q, want %q", trace.EnvelopeID, dispatchRes.EnvelopeID)
	}
	if trace.PeerID != "demo-peer-001" {
		t.Errorf("trace.PeerID = %q, want demo-peer-001", trace.PeerID)
	}
	if trace.Outcome != "ok" {
		t.Errorf("trace.Outcome = %q, want ok", trace.Outcome)
	}
	if trace.Direction != "send" {
		t.Errorf("trace.Direction = %q, want send", trace.Direction)
	}
	if trace.BytesOut <= 0 {
		t.Errorf("trace.BytesOut = %d, want > 0", trace.BytesOut)
	}
	if trace.CreatedAt == "" {
		t.Error("trace.CreatedAt must not be empty")
	}
	// Validate CreatedAt is parseable.
	if _, parseErr := time.Parse(time.RFC3339Nano, trace.CreatedAt); parseErr != nil {
		t.Errorf("trace.CreatedAt %q is not RFC3339Nano: %v", trace.CreatedAt, parseErr)
	}

	// Step 5: verify audit was emitted with correct fields.
	// Give the goroutine a moment to complete (the stream is drained async).
	deadline := time.Now().Add(100 * time.Millisecond)
	var records []emitRecord
	for time.Now().Before(deadline) {
		records = em.snapshot()
		if len(records) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(records) == 0 {
		t.Fatal("expected at least one audit event, got none")
	}
	evt := records[0].event
	if evt.Kind != coreaudit.KindACPEnvelope {
		t.Errorf("audit Kind = %q, want %q", evt.Kind, coreaudit.KindACPEnvelope)
	}
	// Decode payload to validate fields.
	var p coreaudit.ACPEnvelopePayload
	if decErr := json.Unmarshal(evt.Payload, &p); decErr != nil {
		t.Fatalf("unmarshal audit payload: %v", decErr)
	}
	if p.PeerID != "demo-peer-001" {
		t.Errorf("audit.PeerID = %q, want demo-peer-001", p.PeerID)
	}
	if p.Outcome != "ok" {
		t.Errorf("audit.Outcome = %q, want ok", p.Outcome)
	}
	if p.Transport == "" {
		t.Error("audit.Transport must not be empty")
	}

	// Step 6: peer list includes the trusted peer.
	peers, err := api.ACP_ListPeers(ctx)
	if err != nil {
		t.Fatalf("ACP_ListPeers: %v", err)
	}
	if len(peers) == 0 {
		t.Fatal("expected at least one peer, got none")
	}
	found := false
	for _, peer := range peers {
		if peer.ID == "demo-peer-001" {
			found = true
			if peer.TrustTier != "verified" {
				t.Errorf("peer.TrustTier = %q, want verified", peer.TrustTier)
			}
		}
	}
	if !found {
		t.Error("demo-peer-001 not found in ACP_ListPeers result")
	}

	// Step 7: revoke the peer.
	if revokeErr := api.ACP_RevokePeer(ctx, "demo-peer-001"); revokeErr != nil {
		t.Fatalf("ACP_RevokePeer: %v", revokeErr)
	}
	peersAfter, _ := api.ACP_ListPeers(ctx)
	for _, peer := range peersAfter {
		if peer.ID == "demo-peer-001" {
			t.Error("demo-peer-001 still present after ACP_RevokePeer")
		}
	}
}

// TestNullAPI validates the NullAPI returns consistent "not configured" errors.
func TestNullAPI(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	null := acpview.NewNullAPI()

	if peers, err := null.ACP_ListPeers(ctx); err != nil || peers != nil {
		t.Errorf("NullAPI.ACP_ListPeers should return nil/nil; got %v, %v", peers, err)
	}
	if _, err := null.ACP_TrustPeer(ctx, "{}"); err == nil {
		t.Error("NullAPI.ACP_TrustPeer should return error")
	}
	if err := null.ACP_RevokePeer(ctx, "x"); err == nil {
		t.Error("NullAPI.ACP_RevokePeer should return error")
	}
	if _, err := null.ACP_Dispatch(ctx, "x", "y"); err == nil {
		t.Error("NullAPI.ACP_Dispatch should return error")
	}
	if _, err := null.ACP_GetTrace(ctx, "x"); err == nil {
		t.Error("NullAPI.ACP_GetTrace should return error")
	}
}
