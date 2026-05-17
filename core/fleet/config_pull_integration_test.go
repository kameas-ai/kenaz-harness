package fleet

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// integrationApplier is a test ConfigApplier that records bundle sections
// applied and can simulate apply errors.
type integrationApplier struct {
	mu            sync.Mutex
	bundles       []*Bundle
	cedarDeltaIDs []string // team_rule_ids from applied cedar deltas
	mcpAllowlists [][]string
	applyErr      error
}

func (a *integrationApplier) ApplyBundle(_ context.Context, b *Bundle) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	cp := *b
	a.bundles = append(a.bundles, &cp)
	// Extract cedar delta rule IDs.
	if len(b.CedarDelta) > 0 {
		var delta CedarDelta
		if err := json.Unmarshal(b.CedarDelta, &delta); err == nil {
			for ruleID := range delta.Rules {
				a.cedarDeltaIDs = append(a.cedarDeltaIDs, ruleID)
			}
		}
	}
	if b.MCPAllowlist != nil {
		a.mcpAllowlists = append(a.mcpAllowlists, b.MCPAllowlist)
	}
	return a.applyErr
}

func (a *integrationApplier) snapshot() ([]*Bundle, []string, [][]string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	bundles := make([]*Bundle, len(a.bundles))
	copy(bundles, a.bundles)
	cedarIDs := append([]string(nil), a.cedarDeltaIDs...)
	mcp := make([][]string, len(a.mcpAllowlists))
	for i, l := range a.mcpAllowlists {
		mcp[i] = append([]string(nil), l...)
	}
	return bundles, cedarIDs, mcp
}

// integrationFleetServer is a fake fleet server that:
//   - serves GET /api/v1/configs → 200 with signed bundle or 304
//   - serves POST /api/v1/configs/:id/ack → 200 recording ack
type integrationFleetServer struct {
	mu            sync.Mutex
	bundles       [][]byte // sequence of bundle JSONs to serve in order
	bundleIdx     int
	ackPayloads   []configACKPayload
}

func (s *integrationFleetServer) addBundle(data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bundles = append(s.bundles, data)
}

func (s *integrationFleetServer) ackCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.ackPayloads)
}

func (s *integrationFleetServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/ack") {
		var p configACKPayload
		_ = json.NewDecoder(r.Body).Decode(&p)
		s.mu.Lock()
		s.ackPayloads = append(s.ackPayloads, p)
		s.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		return
	}

	// Config fetch.
	s.mu.Lock()
	idx := s.bundleIdx
	var data []byte
	if idx < len(s.bundles) {
		data = s.bundles[idx]
	}
	s.mu.Unlock()

	if data == nil {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	// Check if 304 applies via checksum.
	cs := r.URL.Query().Get("checksum")
	if cs != "" {
		sum := sha256.Sum256(data)
		hexCS := fmt.Sprintf("%x", sum[:])
		if cs == hexCS {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}

	// Advance to next bundle.
	s.mu.Lock()
	if s.bundleIdx < len(s.bundles)-1 {
		s.bundleIdx++
	}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// signAndMarshalBundle signs a bundle and marshals it to JSON.
func signAndMarshalBundle(t *testing.T, priv ed25519.PrivateKey, b *Bundle) []byte {
	t.Helper()
	payload, err := b.signingPayload()
	if err != nil {
		t.Fatalf("signingPayload: %v", err)
	}
	hash := sha256.Sum256(payload)
	sig := ed25519.Sign(priv, hash[:])
	b.Signature = base64.RawStdEncoding.EncodeToString(sig)
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

// TestIntegration_BundleAppliesCedarAndMCP verifies the end-to-end path:
// fake fleet → ConfigPoller fetches → verifies → integrationApplier receives.
func TestIntegration_BundleAppliesCedarAndMCP(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	setTestSigningKey(t, pub)

	bundle := &Bundle{
		BundleID: 10,
		IssuedAt: time.Now(),
		CedarDelta: json.RawMessage(`{"rules":{"team-allow-github":"permit(principal,action,resource);"}}`),
		MCPAllowlist: []string{"github", "slack"},
		ModelPrefs: &BundleModelPrefs{DefaultModel: "anthropic/claude-opus-4"},
	}
	bundleJSON := signAndMarshalBundle(t, priv, bundle)

	fake := &integrationFleetServer{}
	fake.addBundle(bundleJSON)
	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-int",
		RefreshToken: "rt-int",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	client := makeTestClient(t, srv.URL)

	applier := &integrationApplier{}
	dataDir := t.TempDir()
	p := NewConfigPoller(client, dataDir, applier)
	p.interval = 20 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	p.Start(ctx)
	defer p.Stop()

	// Wait for apply.
	if !waitUntil(t, 300*time.Millisecond, func() bool {
		b, _, _ := applier.snapshot()
		return len(b) >= 1
	}) {
		t.Fatal("expected at least one apply")
	}

	bundles, cedarIDs, mcpLists := applier.snapshot()
	if len(bundles) == 0 {
		t.Fatal("no bundles applied")
	}
	if bundles[0].BundleID != 10 {
		t.Errorf("BundleID = %d, want 10", bundles[0].BundleID)
	}

	// Verify cedar delta was decoded.
	found := false
	for _, id := range cedarIDs {
		if id == "team-allow-github" {
			found = true
		}
	}
	if !found {
		t.Errorf("cedar delta team-allow-github not found in applied delta IDs: %v", cedarIDs)
	}

	// Verify MCP allowlist.
	if len(mcpLists) == 0 {
		t.Error("no MCP allowlist applied")
	} else if len(mcpLists[0]) != 2 {
		t.Errorf("MCP allowlist len = %d, want 2", len(mcpLists[0]))
	}

	// Verify ACK was sent.
	if !waitUntil(t, 200*time.Millisecond, func() bool { return fake.ackCount() >= 1 }) {
		t.Error("expected at least one ACK to be sent")
	}

	// Verify status shows bundle applied.
	st := p.Status()
	if st.LastAppliedID != 10 {
		t.Errorf("Status.LastAppliedID = %d, want 10", st.LastAppliedID)
	}
	if st.Source != "fleet" {
		t.Errorf("Status.Source = %q, want fleet", st.Source)
	}
}

// TestIntegration_SignatureRejectionKeepsPrior verifies that a signature
// failure hard-rejects: the applier sees NO call and the status records the error.
func TestIntegration_SignatureRejectionKeepsPrior(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	// Use a DIFFERENT public key for verification.
	wrongPub, _, _ := ed25519.GenerateKey(rand.Reader)
	setTestSigningKey(t, wrongPub)

	bundle := &Bundle{BundleID: 1, IssuedAt: time.Now()}
	bundleJSON := signAndMarshalBundle(t, priv, bundle)

	fake := &integrationFleetServer{}
	fake.addBundle(bundleJSON)
	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-int",
		RefreshToken: "rt-int",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	client := makeTestClient(t, srv.URL)

	applier := &integrationApplier{}
	dataDir := t.TempDir()
	p := NewConfigPoller(client, dataDir, applier)
	p.interval = 20 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	p.Start(ctx)
	<-ctx.Done()
	p.Stop()

	b, _, _ := applier.snapshot()
	if len(b) > 0 {
		t.Errorf("expected no applies on bad signature, got %d", len(b))
	}
	st := p.Status()
	if st.LastError == "" {
		t.Error("expected LastError after signature rejection")
	}
	if !strings.Contains(st.LastError, "verification failed") && !strings.Contains(st.LastError, "signature") {
		t.Errorf("LastError does not mention verification: %q", st.LastError)
	}
}

// TestIntegration_PartialApplyStillACKs verifies that when ApplyBundle returns
// an error (partial failure), the poller still sends an ACK with applied=false.
func TestIntegration_PartialApplyStillACKs(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	setTestSigningKey(t, pub)

	bundle := &Bundle{BundleID: 5, IssuedAt: time.Now()}
	bundleJSON := signAndMarshalBundle(t, priv, bundle)

	fake := &integrationFleetServer{}
	fake.addBundle(bundleJSON)
	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-int",
		RefreshToken: "rt-int",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	client := makeTestClient(t, srv.URL)

	applier := &integrationApplier{
		applyErr: errors.New("cedar engine error: test"),
	}
	dataDir := t.TempDir()
	p := NewConfigPoller(client, dataDir, applier)
	p.interval = 20 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	p.Start(ctx)

	// Wait for at least one apply attempt.
	if !waitUntil(t, 300*time.Millisecond, func() bool {
		b, _, _ := applier.snapshot()
		return len(b) >= 1
	}) {
		t.Fatal("expected at least one apply attempt")
	}
	p.Stop()
	cancel()

	// ACK should have been sent.
	if !waitUntil(t, 200*time.Millisecond, func() bool { return fake.ackCount() >= 1 }) {
		t.Error("expected ACK to be sent even on partial failure")
	}
	// Bundle_id should still have advanced.
	st := p.Status()
	if st.LastAppliedID != 5 {
		t.Errorf("LastAppliedID = %d, want 5 (bundle_id advances even on partial failure)", st.LastAppliedID)
	}
	if !strings.Contains(st.LastError, "partial") {
		t.Errorf("expected partial failure in LastError, got %q", st.LastError)
	}
}
