package fleet

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"
)

// fakeApplier is a race-safe ConfigApplier that records applied bundles.
type fakeApplier struct {
	mu      sync.Mutex
	applied []*Bundle
	errFn   func(*Bundle) error
}

func (f *fakeApplier) ApplyBundle(_ context.Context, b *Bundle) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *b
	f.applied = append(f.applied, &cp)
	if f.errFn != nil {
		return f.errFn(b)
	}
	return nil
}

func (f *fakeApplier) snapshot() []*Bundle {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*Bundle, len(f.applied))
	copy(out, f.applied)
	return out
}

// buildAndSignBundle creates and signs a bundle for testing.
func buildAndSignBundle(t *testing.T, priv ed25519.PrivateKey, id int64) *Bundle {
	t.Helper()
	b := &Bundle{
		BundleID:     id,
		IssuedAt:     time.Now(),
		MCPAllowlist: []string{"github"},
	}
	payload, err := b.signingPayload()
	if err != nil {
		t.Fatalf("signingPayload: %v", err)
	}
	hash := sha256.Sum256(payload)
	sig := ed25519.Sign(priv, hash[:])
	b.Signature = base64.RawStdEncoding.EncodeToString(sig)
	return b
}

func bundleToJSON(t *testing.T, b *Bundle) []byte {
	t.Helper()
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("marshal bundle: %v", err)
	}
	return data
}

// fakeFleetConfigServer handles GET /api/v1/configs (with checksum 304 logic)
// and POST /api/v1/configs/:id/ack.
type fakeFleetConfigServer struct {
	mu       sync.Mutex
	response []byte
	status   int // 0 means derive from presence of response
	ackIDs   []int64
}

func (f *fakeFleetConfigServer) setResponse(data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.response = data
}

func (f *fakeFleetConfigServer) snapshotACKs() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]int64, len(f.ackIDs))
	copy(out, f.ackIDs)
	return out
}

func (f *fakeFleetConfigServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	resp := f.response
	status := f.status
	f.mu.Unlock()

	// ACK endpoint.
	if r.Method == http.MethodPost {
		var payload configACKPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err == nil {
			f.mu.Lock()
			f.ackIDs = append(f.ackIDs, payload.BundleID)
			f.mu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	// Config fetch with checksum-based 304.
	if resp != nil {
		cs := r.URL.Query().Get("checksum")
		sum := sha256.Sum256(resp)
		hexCS := fmt.Sprintf("%x", sum[:])
		if cs == hexCS {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}

	if resp == nil {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	if status != 0 {
		w.WriteHeader(status)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(resp)
}

// setTestSigningKey sets fleetSigningPublicKeyBytes to the hex of pub and
// restores the original on test cleanup.
func setTestSigningKey(t *testing.T, pub ed25519.PublicKey) {
	t.Helper()
	hexStr := ""
	for _, by := range []byte(pub) {
		hexStr += string([]byte{hexEncNibble2(by >> 4), hexEncNibble2(by & 0x0f)})
	}
	saved := fleetSigningPublicKeyBytes
	fleetSigningPublicKeyBytes = hexStr
	t.Cleanup(func() { fleetSigningPublicKeyBytes = saved })
}

func newPollerForTest(t *testing.T, srv *httptest.Server, applier ConfigApplier, dataDir string) *ConfigPoller {
	t.Helper()
	stubTokens(t, TokenSet{
		AccessToken:  "at-test",
		RefreshToken: "rt-test",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	c := makeTestClient(t, srv.URL)
	p := NewConfigPoller(c, dataDir, applier)
	p.interval = 20 * time.Millisecond
	return p
}

// TestConfigPoller_304NoApply verifies that after a first 200 apply, subsequent
// polls see 304 and do not re-apply.
func TestConfigPoller_304NoApply(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	setTestSigningKey(t, pub)

	b1 := buildAndSignBundle(t, priv, 1)
	fake := &fakeFleetConfigServer{response: bundleToJSON(t, b1)}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	applier := &fakeApplier{}
	dataDir := t.TempDir()
	p := newPollerForTest(t, srv, applier, dataDir)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	p.Start(ctx)
	defer p.Stop()

	// Wait for first apply.
	if !waitUntil(t, 300*time.Millisecond, func() bool { return len(applier.snapshot()) >= 1 }) {
		t.Fatal("expected at least one apply, got none")
	}
	firstCount := len(applier.snapshot())

	// Wait another couple of ticks (304 should prevent re-apply).
	time.Sleep(60 * time.Millisecond)
	if len(applier.snapshot()) > firstCount {
		t.Errorf("expected no re-apply on 304; first=%d now=%d", firstCount, len(applier.snapshot()))
	}

	st := p.Status()
	if st.LastAppliedID != 1 {
		t.Errorf("LastAppliedID = %d, want 1", st.LastAppliedID)
	}
}

// TestConfigPoller_200ThenUpdate verifies that a new bundle (higher ID) gets applied.
func TestConfigPoller_200ThenUpdate(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	setTestSigningKey(t, pub)

	b1 := buildAndSignBundle(t, priv, 1)
	fake := &fakeFleetConfigServer{response: bundleToJSON(t, b1)}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	applier := &fakeApplier{}
	dataDir := t.TempDir()
	p := newPollerForTest(t, srv, applier, dataDir)

	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	p.Start(ctx)
	defer p.Stop()

	if !waitUntil(t, 300*time.Millisecond, func() bool { return len(applier.snapshot()) >= 1 }) {
		t.Fatal("expected first apply")
	}

	// Swap to a new bundle.
	b2 := buildAndSignBundle(t, priv, 2)
	fake.setResponse(bundleToJSON(t, b2))

	if !waitUntil(t, 300*time.Millisecond, func() bool { return len(applier.snapshot()) >= 2 }) {
		t.Fatalf("expected second apply, got %d", len(applier.snapshot()))
	}
	if applier.snapshot()[1].BundleID != 2 {
		t.Errorf("second apply BundleID = %d, want 2", applier.snapshot()[1].BundleID)
	}
	st := p.Status()
	if st.LastAppliedID != 2 {
		t.Errorf("status LastAppliedID = %d, want 2", st.LastAppliedID)
	}
}

// TestConfigPoller_BadSignatureHardReject verifies that a bad signature prevents
// any apply and records an error in Status.
func TestConfigPoller_BadSignatureHardReject(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)

	// Sign with priv but verify with a DIFFERENT key.
	pub2, _, _ := ed25519.GenerateKey(rand.Reader)
	setTestSigningKey(t, pub2)

	b := buildAndSignBundle(t, priv, 1)
	fake := &fakeFleetConfigServer{response: bundleToJSON(t, b)}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	applier := &fakeApplier{}
	dataDir := t.TempDir()
	p := newPollerForTest(t, srv, applier, dataDir)

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	p.Start(ctx)
	<-ctx.Done()
	p.Stop()

	if len(applier.snapshot()) > 0 {
		t.Errorf("expected no applies on bad signature, got %d", len(applier.snapshot()))
	}
	st := p.Status()
	if st.LastAppliedID != 0 {
		t.Errorf("LastAppliedID should be 0, got %d", st.LastAppliedID)
	}
	if st.LastError == "" {
		t.Error("expected non-empty LastError after signature failure")
	}
}

// TestConfigPoller_DiskStatePersists verifies that lastAppliedID and checksum
// are persisted to disk.
func TestConfigPoller_DiskStatePersists(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	setTestSigningKey(t, pub)

	b := buildAndSignBundle(t, priv, 7)
	fake := &fakeFleetConfigServer{response: bundleToJSON(t, b)}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	applier := &fakeApplier{}
	dataDir := t.TempDir()
	p := newPollerForTest(t, srv, applier, dataDir)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	p.Start(ctx)

	if !waitUntil(t, 300*time.Millisecond, func() bool { return len(applier.snapshot()) >= 1 }) {
		t.Fatal("expected at least one apply")
	}
	p.Stop()
	cancel()

	id, cs, err := loadBundleState(dataDir)
	if err != nil {
		t.Fatalf("loadBundleState: %v", err)
	}
	if id != 7 {
		t.Errorf("persisted bundle_id = %d, want 7", id)
	}
	if cs == "" {
		t.Error("persisted checksum should be non-empty")
	}
	if _, err := os.Stat(bundleIDPath(dataDir)); err != nil {
		t.Errorf("bundle_id.txt missing: %v", err)
	}
	if _, err := os.Stat(bundleChecksumPath(dataDir)); err != nil {
		t.Errorf("bundle_checksum.txt missing: %v", err)
	}
}

// waitUntil polls fn every 10ms until it returns true or timeout is reached.
func waitUntil(t *testing.T, timeout time.Duration, fn func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fn()
}

func hexEncNibble2(b byte) byte {
	if b < 10 {
		return '0' + b
	}
	return 'a' + b - 10
}
