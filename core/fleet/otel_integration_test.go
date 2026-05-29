package fleet

// integration_test.go — end-to-end pipeline: span → ring buffer → flush →
// fake fleet server verifies signature.
// (fleet-otel-archival-01NDFSEX11 WP07)

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// testEd25519Signer is a deterministic ed25519 signer for integration tests.
// It exposes the public key so the test server can verify signatures.
type testEd25519Signer struct {
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
	fp   string // sha256:<hex> fingerprint
}

func newTestSigner(t *testing.T) *testEd25519Signer {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	fp := pubkeyFingerprint(pub)
	return &testEd25519Signer{priv: priv, pub: pub, fp: fp}
}

func (s *testEd25519Signer) Sign(payload []byte) (sig string, pubkeyFP string, err error) {
	raw := ed25519.Sign(s.priv, payload)
	return base64.StdEncoding.EncodeToString(raw), s.fp, nil
}

// pubKeyBase64 returns the base64-encoded raw 32-byte public key for use with
// VerifySignature.
func (s *testEd25519Signer) pubKeyBase64() string {
	return base64.StdEncoding.EncodeToString(s.pub)
}

// signedBatchEnvelope is the JSON shape the server receives. It has Signature
// and DevicePubkeyFingerprint populated.
type signedBatchEnvelope struct {
	BatchID                 string `json:"batch_id"`
	DevicePubkeyFingerprint string `json:"device_pubkey_fingerprint"`
	ConsentLevel            string `json:"consent_level"`
	Signature               string `json:"signature"`
}

// TestIntegration_SpanToFleetServer is the end-to-end integration test:
//
//   Spans are pushed into FleetSpanExporter → ring buffer →
//   Shutdown triggers a flush → fake fleet server receives the POST →
//   server deserialises the signed batch → verifies the ed25519 signature
//   over the canonical (unsigned) payload body.
//
// (fleet-otel-archival-01NDFSEX11 WP07)
func TestIntegration_SpanToFleetServer(t *testing.T) {
	t.Parallel()

	signer := newTestSigner(t)

	// ── Fake fleet server ──────────────────────────────────────────────────
	type received struct {
		body        []byte
		contentType string
	}
	var mu sync.Mutex
	var requests []received

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/telemetry/otel" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, received{body: body, contentType: r.Header.Get("Content-Type")})
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// ── FleetSpanExporter ─────────────────────────────────────────────────
	exp := NewFleetSpanExporter(FleetExporterConfig{
		Endpoint:      srv.URL,
		Signer:        signer,
		Consent:       &stubConsent{level: "full"},
		FlushInterval: 10 * time.Minute, // prevent timer-driven flush
		NewULID:       func() string { return "integ-batch-1" },
	})

	// Push 3 spans.
	sp := makeSpan(t, "integration-span")
	spans := make([]sdktrace.ReadOnlySpan, 3)
	for i := range spans {
		spans[i] = sp
	}
	if err := exp.ExportSpans(context.Background(), spans); err != nil {
		t.Fatalf("ExportSpans: %v", err)
	}

	// Shutdown triggers the final flush.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := exp.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	// ── Verify the server received at least one POST ────────────────────
	mu.Lock()
	reqs := make([]received, len(requests))
	copy(reqs, requests)
	mu.Unlock()

	if len(reqs) == 0 {
		t.Fatal("want at least 1 POST to /api/v1/telemetry/otel, got 0")
	}

	// ── Verify the signed batch on the first request ───────────────────
	req := reqs[0]
	if req.contentType != "application/json" {
		t.Errorf("content-type want application/json, got %q", req.contentType)
	}

	var env signedBatchEnvelope
	if err := json.Unmarshal(req.body, &env); err != nil {
		t.Fatalf("unmarshal batch: %v", err)
	}

	// The signature covers the unsigned JSON body (Signature and
	// DevicePubkeyFingerprint zeroed). To reconstruct it we unmarshal into
	// a map, delete the two signature fields, and re-marshal.
	var rawMap map[string]any
	if err := json.Unmarshal(req.body, &rawMap); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}
	delete(rawMap, "signature")
	delete(rawMap, "device_pubkey_fingerprint")
	// The exporter sets these to "" before signing (zero value); we set
	// them back to "" to reconstruct the exact canonical bytes.
	rawMap["signature"] = ""
	rawMap["device_pubkey_fingerprint"] = ""
	// Re-serialise via the original fleetBatch round-trip: to get the exact
	// same bytes the exporter signed, unmarshal the body into a fleetBatch,
	// zero the sig fields, and re-marshal.
	var signedBatch fleetBatch
	if err := json.Unmarshal(req.body, &signedBatch); err != nil {
		t.Fatalf("unmarshal to fleetBatch: %v", err)
	}
	unsignedBatch := fleetBatch{
		BatchID:      signedBatch.BatchID,
		ConsentLevel: signedBatch.ConsentLevel,
		Spans:        signedBatch.Spans,
		Metrics:      signedBatch.Metrics,
		Logs:         signedBatch.Logs,
		// Signature and DevicePubkeyFingerprint are zero → empty string.
	}
	canonicalPayload, err := json.Marshal(unsignedBatch)
	if err != nil {
		t.Fatalf("re-marshal unsigned batch: %v", err)
	}

	ok, err := VerifySignature(signer.pubKeyBase64(), canonicalPayload, env.Signature)
	if err != nil {
		t.Fatalf("VerifySignature error: %v", err)
	}
	if !ok {
		t.Error("signature verification failed for fleet telemetry batch")
	}

	// ── Check batch_id is non-empty ────────────────────────────────────
	if env.BatchID == "" {
		t.Error("want non-empty batch_id")
	}

	// ── Check consent_level is forwarded ──────────────────────────────
	if env.ConsentLevel != "full" {
		t.Errorf("want consent_level=full, got %q", env.ConsentLevel)
	}
}

// TestIntegration_ConsentNone_NoPost verifies that no HTTP POST is made when
// consent is "none" — the pipeline is silent.
func TestIntegration_ConsentNone_NoPost(t *testing.T) {
	t.Parallel()
	var callCount int
	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		callCount++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	signer := newTestSigner(t)
	exp := NewFleetSpanExporter(FleetExporterConfig{
		Endpoint:      srv.URL,
		Signer:        signer,
		Consent:       &stubConsent{level: "none"},
		FlushInterval: 10 * time.Minute,
		NewULID:       func() string { return "no-post-1" },
	})

	sp := makeSpan(t, "s")
	_ = exp.ExportSpans(context.Background(), []sdktrace.ReadOnlySpan{sp})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = exp.Shutdown(ctx)

	mu.Lock()
	n := callCount
	mu.Unlock()
	if n != 0 {
		t.Errorf("want 0 HTTP calls when consent=none, got %d", n)
	}
}
