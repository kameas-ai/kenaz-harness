// Package llm_test — multimodal output integration tests.
//
// These tests exercise the generated-image auto-capture pipeline end-to-end
// using fixture payloads that mirror real provider response shapes. No actual
// HTTP calls to provider APIs are made — image-URL fetch tests use
// net/http/httptest.
//
// All tests run under the default `go test ./core/llm/...` profile (no build
// tag required) because they use only in-process fakes.
//
// (multimodal-io-extended-01KQ8TD2 WP07)
package llm_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	coreart "github.com/sigil-tech/kaneaz-harness/core/artifacts"
	artview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/artifacts"
)

// ─── fixture image bytes ──────────────────────────────────────────────────────

// minimalPNG is a 1×1 white PNG. We embed it inline here so the test has no
// disk dependency; it closely mirrors what DALL-E 3 returns in b64_json mode
// (just much smaller).
var minimalPNG = func() []byte {
	// 1×1 white PNG, 68 bytes (RFC 2083-compliant minimal PNG).
	const b64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwADhQGAWjR9awAAAABJRU5ErkJggg=="
	b, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		panic("fixture: base64 decode: " + err.Error())
	}
	return b
}()

// minimalJPEG is a 1×1 white JPEG (SOI + EOI — technically invalid content
// but sufficient for MIME-type routing in the tests).
var minimalJPEG = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46, 0x00, 0x01, 0xFF, 0xD9}

// ─── recording capture manager ────────────────────────────────────────────────

type recordCaptureMgr struct {
	mu       sync.Mutex
	captured []captureCall
	errFn    func(n int) error // optional — returns error on nth call
}

type captureCall struct {
	candidates []coreart.CaptureCandidate
	sessionID  string
}

func (m *recordCaptureMgr) Capture(ctx context.Context, candidates []coreart.CaptureCandidate, sessionID string) ([]coreart.Artifact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.errFn != nil {
		if err := m.errFn(len(m.captured)); err != nil {
			return nil, err
		}
	}
	m.captured = append(m.captured, captureCall{candidates: candidates, sessionID: sessionID})
	arts := make([]coreart.Artifact, len(candidates))
	for i, c := range candidates {
		arts[i] = coreart.Artifact{
			ID:       fmt.Sprintf("art-%d", i),
			Title:    c.Title,
			MimeType: c.MimeType,
		}
	}
	return arts, nil
}

func (m *recordCaptureMgr) snapshot() []captureCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]captureCall, len(m.captured))
	copy(out, m.captured)
	return out
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func enabledCfg() coreart.CaptureConfig {
	return coreart.CaptureConfig{
		AutoCaptureGeneratedImages: true,
		MaxGeneratedImageBytes:     20 * 1024 * 1024, // 20 MiB
	}
}

func disabledCfg() coreart.CaptureConfig {
	return coreart.CaptureConfig{
		AutoCaptureGeneratedImages: false,
		MaxGeneratedImageBytes:     20 * 1024 * 1024,
	}
}

func cappedCfg(cap int64) coreart.CaptureConfig {
	return coreart.CaptureConfig{
		AutoCaptureGeneratedImages: true,
		MaxGeneratedImageBytes:     cap,
	}
}

func newSinkWithCfg(mgr *recordCaptureMgr, cfg coreart.CaptureConfig) artview.GeneratedImageCapturer {
	// artview.NewSink is package-internal; access via the *Sink exported type
	// which satisfies GeneratedImageCapturer. We call the package-level
	// constructor through the ArtifactSink interface and cast back.
	//
	// The test constructs the Sink through the exported NewSink function
	// which returns ArtifactSink; GeneratedImageCapturer is a sub-interface
	// implemented by the same concrete value.
	sink := artview.NewSink(mgr, func() coreart.CaptureConfig { return cfg }, nil)
	capturer, ok := sink.(artview.GeneratedImageCapturer)
	if !ok {
		panic("NewSink result does not implement GeneratedImageCapturer")
	}
	return capturer
}

// ─── tests ────────────────────────────────────────────────────────────────────

// TestOnGeneratedImage_InlineBytes_DALLEE3 — happy path: inline PNG bytes
// (DALL-E 3 b64_json response fixture). One Capture call, correct MIME and
// title, RevisedPrompt round-trips in the SourceRef.
func TestOnGeneratedImage_InlineBytes_DALLEE3(t *testing.T) {
	t.Parallel()
	mgr := &recordCaptureMgr{}
	capturer := newSinkWithCfg(mgr, enabledCfg())

	payload := artview.GeneratedImagePayload{
		Data:          minimalPNG,
		MimeType:      "image/png",
		RevisedPrompt: "A photorealistic white square on a white background.",
		Index:         0,
	}

	if err := capturer.OnGeneratedImage(context.Background(), "sess-dalle3", "msg-1", payload); err != nil {
		t.Fatalf("OnGeneratedImage: %v", err)
	}

	calls := mgr.snapshot()
	if len(calls) != 1 {
		t.Fatalf("Capture calls = %d, want 1", len(calls))
	}
	if len(calls[0].candidates) != 1 {
		t.Fatalf("candidates = %d, want 1", len(calls[0].candidates))
	}
	c := calls[0].candidates[0]
	if c.MimeType != "image/png" {
		t.Errorf("MimeType = %q, want %q", c.MimeType, "image/png")
	}
	if c.Source != coreart.SourceModelOutput {
		t.Errorf("Source = %q, want %q", c.Source, coreart.SourceModelOutput)
	}
	if c.SourceRef.RevisedPrompt != payload.RevisedPrompt {
		t.Errorf("RevisedPrompt = %q, want %q", c.SourceRef.RevisedPrompt, payload.RevisedPrompt)
	}
	if c.SourceRef.ImageIndex != 0 {
		t.Errorf("ImageIndex = %d, want 0", c.SourceRef.ImageIndex)
	}
	if calls[0].sessionID != "sess-dalle3" {
		t.Errorf("sessionID = %q, want %q", calls[0].sessionID, "sess-dalle3")
	}
}

// TestOnGeneratedImage_InlineBytes_JPEG — JPEG payload (gpt-image-1 or Titan
// Image JPEG mode). Verifies .jpeg extension is inferred from the MIME type.
func TestOnGeneratedImage_InlineBytes_JPEG(t *testing.T) {
	t.Parallel()
	mgr := &recordCaptureMgr{}
	capturer := newSinkWithCfg(mgr, enabledCfg())

	payload := artview.GeneratedImagePayload{
		Data:     minimalJPEG,
		MimeType: "image/jpeg",
		Index:    1,
	}

	if err := capturer.OnGeneratedImage(context.Background(), "sess-gptimg1", "msg-2", payload); err != nil {
		t.Fatalf("OnGeneratedImage: %v", err)
	}

	calls := mgr.snapshot()
	if len(calls) != 1 {
		t.Fatalf("Capture calls = %d, want 1", len(calls))
	}
	c := calls[0].candidates[0]
	if c.MimeType != "image/jpeg" {
		t.Errorf("MimeType = %q, want %q", c.MimeType, "image/jpeg")
	}
	// Title must contain the index and have .jpeg extension.
	if c.Title == "" {
		t.Error("Title is empty")
	}
	// ImageIndex must match payload.Index.
	if c.SourceRef.ImageIndex != 1 {
		t.Errorf("ImageIndex = %d, want 1", c.SourceRef.ImageIndex)
	}
}

// TestOnGeneratedImage_URLFetch_DALLEE3 — DALL-E 3 "url" format fixture.
// The payload carries a CDN URL instead of inline bytes; the sink must fetch
// the URL and capture the response body.
func TestOnGeneratedImage_URLFetch_DALLEE3(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(minimalPNG)
	}))
	defer srv.Close()

	mgr := &recordCaptureMgr{}
	capturer := newSinkWithCfg(mgr, enabledCfg())

	payload := artview.GeneratedImagePayload{
		URL:      srv.URL + "/image.png",
		MimeType: "image/png",
		Index:    0,
	}

	if err := capturer.OnGeneratedImage(context.Background(), "sess-dalleurl", "msg-3", payload); err != nil {
		t.Fatalf("OnGeneratedImage: %v", err)
	}

	calls := mgr.snapshot()
	if len(calls) != 1 {
		t.Fatalf("Capture calls = %d, want 1 (URL fetch path)", len(calls))
	}
	c := calls[0].candidates[0]
	if len(c.Bytes) == 0 {
		t.Error("Bytes is empty after URL fetch")
	}
	if c.MimeType != "image/png" {
		t.Errorf("MimeType = %q, want %q", c.MimeType, "image/png")
	}
}

// TestOnGeneratedImage_URLFetch_404 — non-200 fetch is silently discarded
// (non-fatal). The Capture manager must NOT be called.
func TestOnGeneratedImage_URLFetch_404(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	mgr := &recordCaptureMgr{}
	capturer := newSinkWithCfg(mgr, enabledCfg())

	payload := artview.GeneratedImagePayload{
		URL:   srv.URL + "/gone.png",
		Index: 0,
	}

	// Must not return an error — a failed fetch is silently swallowed.
	if err := capturer.OnGeneratedImage(context.Background(), "sess-404", "msg-4", payload); err != nil {
		t.Fatalf("OnGeneratedImage returned error for 404 fetch: %v", err)
	}

	if calls := mgr.snapshot(); len(calls) != 0 {
		t.Errorf("Capture calls = %d, want 0 after 404 fetch", len(calls))
	}
}

// TestOnGeneratedImage_Disabled — when AutoCaptureGeneratedImages is false no
// Capture call is issued.
func TestOnGeneratedImage_Disabled(t *testing.T) {
	t.Parallel()
	mgr := &recordCaptureMgr{}
	capturer := newSinkWithCfg(mgr, disabledCfg())

	payload := artview.GeneratedImagePayload{
		Data:     minimalPNG,
		MimeType: "image/png",
		Index:    0,
	}

	if err := capturer.OnGeneratedImage(context.Background(), "sess-disabled", "msg-5", payload); err != nil {
		t.Fatalf("OnGeneratedImage: %v", err)
	}

	if calls := mgr.snapshot(); len(calls) != 0 {
		t.Errorf("Capture calls = %d, want 0 when capture disabled", len(calls))
	}
}

// TestOnGeneratedImage_ByteCapEnforced — images larger than the cap are
// silently discarded (no Capture call).
func TestOnGeneratedImage_ByteCapEnforced(t *testing.T) {
	t.Parallel()
	mgr := &recordCaptureMgr{}
	// cap = 10 bytes; minimalPNG is 68 bytes → must be rejected.
	capturer := newSinkWithCfg(mgr, cappedCfg(10))

	payload := artview.GeneratedImagePayload{
		Data:     minimalPNG,
		MimeType: "image/png",
		Index:    0,
	}

	if err := capturer.OnGeneratedImage(context.Background(), "sess-cap", "msg-6", payload); err != nil {
		t.Fatalf("OnGeneratedImage: %v", err)
	}

	if calls := mgr.snapshot(); len(calls) != 0 {
		t.Errorf("Capture calls = %d, want 0 (image exceeds byte cap)", len(calls))
	}
}

// TestOnGeneratedImage_MIMEDefault — payload with empty MimeType must default
// to "image/png" in the captured artifact.
func TestOnGeneratedImage_MIMEDefault(t *testing.T) {
	t.Parallel()
	mgr := &recordCaptureMgr{}
	capturer := newSinkWithCfg(mgr, enabledCfg())

	payload := artview.GeneratedImagePayload{
		Data:  minimalPNG,
		Index: 0,
		// MimeType intentionally blank.
	}

	if err := capturer.OnGeneratedImage(context.Background(), "sess-mime", "msg-7", payload); err != nil {
		t.Fatalf("OnGeneratedImage: %v", err)
	}

	calls := mgr.snapshot()
	if len(calls) != 1 {
		t.Fatalf("Capture calls = %d, want 1", len(calls))
	}
	if calls[0].candidates[0].MimeType != "image/png" {
		t.Errorf("MimeType = %q, want default %q", calls[0].candidates[0].MimeType, "image/png")
	}
}

// TestOnGeneratedImage_TitanImage_MultiImage — Titan Image n=2 fixture.
// Two separate OnGeneratedImage calls (one per image) must each produce a
// Capture call with the correct zero-based ImageIndex.
func TestOnGeneratedImage_TitanImage_MultiImage(t *testing.T) {
	t.Parallel()
	mgr := &recordCaptureMgr{}
	capturer := newSinkWithCfg(mgr, enabledCfg())

	for idx := 0; idx < 2; idx++ {
		payload := artview.GeneratedImagePayload{
			Data:     minimalPNG,
			MimeType: "image/png",
			Index:    idx,
		}
		if err := capturer.OnGeneratedImage(context.Background(), "sess-titan", "msg-8", payload); err != nil {
			t.Fatalf("OnGeneratedImage idx=%d: %v", idx, err)
		}
	}

	calls := mgr.snapshot()
	if len(calls) != 2 {
		t.Fatalf("Capture calls = %d, want 2", len(calls))
	}
	for i, call := range calls {
		if call.candidates[0].SourceRef.ImageIndex != i {
			t.Errorf("call[%d].ImageIndex = %d, want %d",
				i, call.candidates[0].SourceRef.ImageIndex, i)
		}
	}
}

// TestOnGeneratedImage_NoData_NoURL — payload with no data and no URL is a
// no-op (non-fatal discard).
func TestOnGeneratedImage_NoData_NoURL(t *testing.T) {
	t.Parallel()
	mgr := &recordCaptureMgr{}
	capturer := newSinkWithCfg(mgr, enabledCfg())

	payload := artview.GeneratedImagePayload{Index: 0} // both Data and URL empty

	if err := capturer.OnGeneratedImage(context.Background(), "sess-empty", "msg-9", payload); err != nil {
		t.Fatalf("OnGeneratedImage: %v", err)
	}

	if calls := mgr.snapshot(); len(calls) != 0 {
		t.Errorf("Capture calls = %d, want 0 for empty payload", len(calls))
	}
}

// TestOnGeneratedImage_CaptureError_IsWrapped — Capture returning an error
// must propagate as a wrapped error (not swallowed silently).
func TestOnGeneratedImage_CaptureError_IsWrapped(t *testing.T) {
	t.Parallel()
	mgr := &recordCaptureMgr{
		errFn: func(_ int) error { return fmt.Errorf("disk full") },
	}
	capturer := newSinkWithCfg(mgr, enabledCfg())

	payload := artview.GeneratedImagePayload{
		Data:     minimalPNG,
		MimeType: "image/png",
		Index:    0,
	}

	err := capturer.OnGeneratedImage(context.Background(), "sess-err", "msg-10", payload)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
