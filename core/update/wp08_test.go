package update

// wp08_test.go — controls-and-readouts-that-tell-the-truth-01PMZ808 UNIT-4 /
// WP08. AC-020: with a live BackgroundPoll (not a direct checkChannel
// call) given channel="prerelease" and a 404 on the prerelease manifest,
// the fallback branch at checkChannel executes. Before WP07 (same
// mission), BackgroundPoll's channel argument was hardcoded to "stable"
// at every production call site, so this path was structurally
// unreachable outside a test calling checkChannel directly (see
// TestService_PrereleaseFallback in service_test.go, which does exactly
// that). This test drives the real BackgroundPoll entrypoint instead and
// observes the published Info, proving the whole chain — not just
// checkChannel in isolation.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// wp08RecordingPublisher is a race-safe Publisher fake — writes happen on
// BackgroundPoll's own goroutine, reads happen from the test body, so per
// CLAUDE.md's race-safe-test-fakes pattern it needs a mutex + a snapshot
// helper.
type wp08RecordingPublisher struct {
	mu       sync.Mutex
	payloads []Info
}

func (p *wp08RecordingPublisher) Publish(_ string, payload any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if info, ok := payload.(Info); ok {
		p.payloads = append(p.payloads, info)
	}
}

func (p *wp08RecordingPublisher) snapshot() []Info {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Info, len(p.payloads))
	copy(out, p.payloads)
	return out
}

// TestStableManifestURL_MatchesFrontendConstant pins stableManifestURL's
// literal value. It cannot read frontend/src/components/updates/
// useUpdateStore.ts's MANIFEST_URL constant directly (no cross-language
// shared source exists), so this is the Go-side half of a same-value
// pin whose TS-side half is useUpdateStore.fallback.spec.ts's
// "MANIFEST_URL (AC-018)" describe block — see the doc comment on
// stableManifestURL above. If either literal changes, the corresponding
// test must be updated by hand; neither catches drift in the other
// automatically.
func TestStableManifestURL_MatchesFrontendConstant(t *testing.T) {
	const want = "https://downloads.kameas.ai/kenaz-harness/manifest.json"
	if stableManifestURL != want {
		t.Fatalf("stableManifestURL = %q, want %q (must match useUpdateStore.ts's MANIFEST_URL)", stableManifestURL, want)
	}
}

func TestWP08_AC020_BackgroundPollPrereleaseChannel_ReachesFallback(t *testing.T) {
	_, sha := fakeBinary(t, []byte("x"))
	stableBody, _ := json.Marshal(manifest{
		Version: "v0.4.0",
		Assets:  []manifestAsset{{Platform: "linux/amd64", URL: "u", Sha256: sha}},
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/downloads.kameas.ai/kenaz-harness/manifest.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(stableBody)
	})
	mux.HandleFunc("/stage-downloads.kameas.ai/kenaz-harness/manifest.json", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	pub := &wp08RecordingPublisher{}
	cfg := Config{
		CurrentVersion: "v0.3.0",
		DataDir:        t.TempDir(),
		HTTPClient:     &http.Client{Timeout: 5 * time.Second},
		Platform:       "linux/amd64",
		Swapper:        &fakeSwapper{},
		Publisher:      pub,
	}
	svc, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	s := svc.(*service)
	s.cfg.ManifestURL = "" // ensure the production URL-resolution path runs
	s.client = &http.Client{
		Transport: &rewriteTransport{base: srv.URL},
		Timeout:   5 * time.Second,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	// BackgroundPoll's first tick runs immediately (per its own doc
	// comment). Called with channel="prerelease" — the value WP07 now
	// threads from Settings.UpdateChannel instead of the old hardcoded
	// "stable" — this is the production entrypoint, not checkChannel
	// called directly.
	err = s.BackgroundPoll(ctx, 50*time.Millisecond, "prerelease")
	if err != nil && err != context.DeadlineExceeded {
		t.Fatalf("BackgroundPoll: %v", err)
	}

	payloads := pub.snapshot()
	if len(payloads) == 0 {
		t.Fatalf("expected BackgroundPoll(\"prerelease\") to publish at least one Info via the fallback-resolved stable manifest, got none")
	}
	got := payloads[0]
	if got.AvailableVersion != "v0.4.0" {
		t.Fatalf("published Info.AvailableVersion = %q, want %q (from the STABLE manifest — only reachable if the prerelease 404 fallback ran)", got.AvailableVersion, "v0.4.0")
	}
}
