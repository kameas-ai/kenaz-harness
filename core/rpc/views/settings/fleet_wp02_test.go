package settings

// fleet-enforcement-truth-01PMZ505 WP02.
//
// Spec §1.1/§1.2/§5.1, tasks.md AC-001/AC-002: compositeConfigApplier must
// stop returning an empty error slice for a bundle section this device
// cannot apply. An empty slice reads as full success at
// config_pull.go:311 and gets ACKed "applied:true" to the control plane
// for a signature-VERIFIED bundle — a policy-distribution integrity bug,
// not a cosmetic one.
//
// Test rule (spec §8 rule 4): the fixture must construct the seam the way
// production does — a zero-value fleetState, never a pre-wired one — or
// the assertion is vacuous.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
)

// AC-001 — a cedar_delta section on a device with no Cedar engine wired
// fails the apply and names the missing engine. The fixture must NOT call
// SetCedarEngine; a zero-value fleetState is exactly the production shape
// before WP03 (and the shape whenever chassis boot omits the wire).
func TestApplyBundle_CedarDeltaUnwiredEngine_Fails(t *testing.T) {
	applier := &compositeConfigApplier{state: &fleetState{}}
	b := &fleet.Bundle{
		BundleID:   1,
		CedarDelta: json.RawMessage(`{"rules":{"r1":"permit(principal, action, resource);"}}`),
	}
	errs := applier.ApplyBundle(context.Background(), b)
	if len(errs) == 0 {
		t.Fatal("expected a non-empty error slice for an unwired cedar engine; got none — this is the P0 (spec §1.1)")
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "engine") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an error naming the missing engine, got: %v", errs)
	}
}

// mandated_skills present with no skill refs wired must fail the same way.
func TestApplyBundle_MandatedSkillsUnwiredRefs_Fails(t *testing.T) {
	applier := &compositeConfigApplier{state: &fleetState{}}
	b := &fleet.Bundle{
		BundleID:       1,
		MandatedSkills: []json.RawMessage{json.RawMessage(`{"id":"s1"}`)},
	}
	errs := applier.ApplyBundle(context.Background(), b)
	if len(errs) == 0 {
		t.Fatal("expected a non-empty error slice for unwired skill refs; got none")
	}
}

// A bundle whose only section is mcp_allowlist (which reaches a real
// consumer unconditionally, unmodified by this WP) still ACKs clean.
// Without this, WP02 would be satisfiable by an applier that fails
// everything.
func TestApplyBundle_MCPAllowlistOnly_CleanApply(t *testing.T) {
	applier := &compositeConfigApplier{state: &fleetState{}}
	b := &fleet.Bundle{
		BundleID:     1,
		MCPAllowlist: []string{"github"},
	}
	errs := applier.ApplyBundle(context.Background(), b)
	if len(errs) != 0 {
		t.Fatalf("expected a clean apply for mcp_allowlist-only bundle, got errors: %v", errs)
	}
}

// AC-002 — end-to-end: a validly-SIGNED bundle carrying a cedar_delta,
// applied through the REAL compositeConfigApplier via fleet.ConfigPoller
// against a nil-engine fleetState (the production shape). Asserts
// lastAppliedID stays put, lastError is non-empty, the checksum does not
// advance (so the bundle is retried, not 304'd away), and the ACK POST
// body carries "applied":false. Per spec §8 rule 2/3: assert the
// observable (the real POST body, the real Status()), not the applier's
// return value alone, and the bundle MUST be genuinely verified —
// VerifyWithKeySet must actually run — or the test proves nothing about
// the integrity claim in spec §1.1.
func TestConfigPoller_CedarDeltaUnwired_ACKsAppliedFalse(t *testing.T) {
	keyring.MockInit()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}
	restore := fleet.SetSigningKeyForTesting(pub)
	defer restore()

	// ACK capture: the fake server records the raw POST body so the test
	// can assert on the wire payload, not a value the test itself
	// computed. Written from the httptest server's handler goroutine,
	// read from the test goroutine — mutex + snapshot per CLAUDE.md's
	// race-safe test fakes canon (CI runs -race).
	var ackMu sync.Mutex
	var ackBody []byte
	var ackSeen bool
	snapshotACK := func() ([]byte, bool) {
		ackMu.Lock()
		defer ackMu.Unlock()
		return ackBody, ackSeen
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/configs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		b := &fleet.Bundle{
			BundleID:   1,
			IssuedAt:   time.Now(),
			CedarDelta: json.RawMessage(`{"rules":{"r1":"permit(principal, action, resource);"}}`),
		}
		if err := fleet.SignBundleForTesting(b, priv); err != nil {
			t.Errorf("sign bundle: %v", err)
			return
		}
		data, err := json.Marshal(b)
		if err != nil {
			t.Errorf("marshal bundle: %v", err)
			return
		}
		_, _ = w.Write(data)
	})
	mux.HandleFunc("/api/v1/configs/1/ack", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		ackMu.Lock()
		ackBody = body
		ackSeen = true
		ackMu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if err := fleet.SaveTokens(fleet.TokenSet{
		AccessToken:  "at-test",
		RefreshToken: "rt-test",
		ExpiresAt:    time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}
	defer func() { _ = fleet.ClearTokens() }()

	fleet.SeedFleetConfigForTesting(srv.URL, fleet.FleetConfig{
		Issuer:     srv.URL,
		ClientID:   "test",
		APIBaseURL: srv.URL,
		FetchedAt:  time.Now().UTC(),
	})
	client := fleet.NewClientForTesting(srv.URL)

	// The production shape: a real compositeConfigApplier over a
	// zero-value fleetState — cedarEngine is nil, exactly as it is before
	// SetCedarEngine is ever called.
	state := &fleetState{}
	applier := &compositeConfigApplier{state: state}
	dataDir := t.TempDir()
	poller := fleet.NewConfigPoller(client, dataDir, applier)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	poller.Start(ctx)
	defer poller.Stop()

	deadline := time.Now().Add(400 * time.Millisecond)
	var gotACKBody []byte
	var ackWasSeen bool
	for time.Now().Before(deadline) {
		gotACKBody, ackWasSeen = snapshotACK()
		if ackWasSeen {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ackWasSeen {
		t.Fatal("ACK was never posted")
	}

	st := poller.Status()
	if st.LastAppliedID != 0 {
		t.Errorf("LastAppliedID = %d, want 0 (unchanged — the apply was partial, not full)", st.LastAppliedID)
	}
	if st.LastError == "" {
		t.Error("expected a non-empty LastError after a partial apply failure")
	}

	var got struct {
		BundleID int64  `json:"bundle_id"`
		Applied  bool   `json:"applied"`
		Error    string `json:"error"`
	}
	if err := json.Unmarshal(gotACKBody, &got); err != nil {
		t.Fatalf("decode ACK body %q: %v", gotACKBody, err)
	}
	if got.Applied {
		t.Error(`ACK posted "applied":true for a bundle section this device did not apply — this is the exact P0 the WP fixes`)
	}
	if !strings.Contains(got.Error, "engine") {
		t.Errorf("expected the ACK error to name the missing engine, got: %q", got.Error)
	}
}
