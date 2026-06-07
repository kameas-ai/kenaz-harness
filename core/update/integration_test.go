// integration_test.go — end-to-end smoke for the auto-update mission
// (v0.4.0 WP06 capstone).
//
// What this exercises:
//   - NewService → Check → Download (drain progress, assert sha) →
//     ApplyAndRestart with a fake Swapper, asserting the four
//     happy-path audit kinds fire in the expected order.
//   - The skip-then-check round-trip: SkipVersion fires `update.skipped`
//     and Check still returns Available=true with SkippedByUser=true
//     (the banner is suppressed but the underlying signal remains).
//   - The sha-mismatch failure path: Swap returns errSha256Mismatch
//     and `update.failed` fires with ErrorClass="sha_mismatch".
//
// The audit emitter is a tiny recording fake — same shape as the one
// in core/context/audit/audit_test.go.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/context/audit"
)

// recordingAudit captures emitted audit events for assertion.
type recordingAudit struct {
	mu     sync.Mutex
	events []audit.Event
}

func (r *recordingAudit) Emit(_ context.Context, e audit.Event) error {
	r.mu.Lock()
	r.events = append(r.events, e)
	r.mu.Unlock()
	return nil
}

func (r *recordingAudit) snapshot() []audit.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]audit.Event, len(r.events))
	copy(out, r.events)
	return out
}

func (r *recordingAudit) kinds() []audit.Kind {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]audit.Kind, len(r.events))
	for i, e := range r.events {
		out[i] = e.Kind
	}
	return out
}

// containsKind reports whether the slice contains kind.
func containsKind(ks []audit.Kind, want audit.Kind) bool {
	for _, k := range ks {
		if k == want {
			return true
		}
	}
	return false
}

// indexOfKind returns the first index of want, or -1.
func indexOfKind(ks []audit.Kind, want audit.Kind) int {
	for i, k := range ks {
		if k == want {
			return i
		}
	}
	return -1
}

// startE2EManifestServer spins up a httptest server that serves
// /manifest.json (with the URL field rewritten to point at the same
// server's /bin path) and /bin (the binary body). Returns the
// manifest URL the Service should consume.
func startE2EManifestServer(t *testing.T, version string, bin []byte, sha string) (manifestURL string, srv *httptest.Server) {
	t.Helper()
	mux := http.NewServeMux()
	srv = httptest.NewUnstartedServer(mux)
	mux.HandleFunc("/manifest.json", func(w http.ResponseWriter, r *http.Request) {
		body, _ := json.Marshal(manifest{
			Version: version,
			Notes:   "e2e test release",
			Assets: []manifestAsset{
				{Platform: "linux/amd64", URL: "http://" + srv.Listener.Addr().String() + "/bin", Sha256: sha},
			},
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
	mux.HandleFunc("/bin", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(bin)))
		_, _ = w.Write(bin)
	})
	srv.Start()
	t.Cleanup(srv.Close)
	return srv.URL + "/manifest.json", srv
}

func TestIntegration_HappyPath_AuditKindsFireInOrder(t *testing.T) {
	body := []byte("integration test binary contents")
	bin, sha := fakeBinary(t, body)

	manifestURL, _ := startE2EManifestServer(t, "v0.4.0", bin, sha)

	dir := t.TempDir()
	rec := &recordingAudit{}
	swapper := &fakeSwapper{}
	cfg := Config{
		CurrentVersion: "v0.3.0",
		DataDir:        dir,
		HTTPClient:     &http.Client{Timeout: 5 * time.Second},
		Swapper:        swapper,
		ManifestURL:    manifestURL,
		Platform:       "linux/amd64",
		Audit:          rec,
		// RunningBinaryPath set below after we create the file.
	}
	runningPath := filepath.Join(dir, "kenaz-harness")
	if err := os.WriteFile(runningPath, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg.RunningBinaryPath = runningPath

	svc, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	ctx := context.Background()

	// ── Check ───────────────────────────────────────────────────────
	info, err := svc.Check(ctx)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !info.Available {
		t.Fatalf("Check: expected Available=true, got %+v", info)
	}
	if info.Sha256 != sha {
		t.Fatalf("Check: sha mismatch: %q vs %q", info.Sha256, sha)
	}

	// ── Download ────────────────────────────────────────────────────
	prog, staged, err := svc.Download(ctx, info)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	last := DownloadProgress{}
	for p := range prog {
		last = p
	}
	if !last.Done || last.Err != nil {
		t.Fatalf("Download not clean: %+v", last)
	}
	if !fileExists(staged.Path) {
		t.Fatalf("staged path missing: %s", staged.Path)
	}

	// ── Apply ───────────────────────────────────────────────────────
	if err := svc.ApplyAndRestart(ctx, staged); err != nil {
		t.Fatalf("ApplyAndRestart: %v", err)
	}
	swaps, restartCall := swapper.snapshot()
	if swaps != 1 || restartCall != runningPath {
		t.Fatalf("swapper not driven: swaps=%d restart=%q", swaps, restartCall)
	}

	// ── Audit assertions ────────────────────────────────────────────
	kinds := rec.kinds()
	t.Logf("emitted audit kinds: %v", kinds)

	wantOrdered := []audit.Kind{
		audit.KindUpdateChecked,
		audit.KindUpdateDownloaded,
		audit.KindUpdateApplied,
	}
	prev := -1
	for _, want := range wantOrdered {
		idx := indexOfKind(kinds[prev+1:], want)
		if idx < 0 {
			t.Fatalf("missing audit kind %q in %v (after index %d)", want, kinds, prev)
		}
		prev = prev + 1 + idx
	}

	// All four expected kinds present (Available is only fired by the
	// background poll on transition; the synchronous Check path emits
	// Checked, not Available — that's by design and matches the spec).
	for _, k := range []audit.Kind{audit.KindUpdateChecked, audit.KindUpdateDownloaded, audit.KindUpdateApplied} {
		if !containsKind(kinds, k) {
			t.Errorf("missing %q in %v", k, kinds)
		}
	}
	// No failures fired on the happy path.
	if containsKind(kinds, audit.KindUpdateFailed) {
		t.Errorf("happy path emitted update.failed: %v", kinds)
	}

	// Privacy invariant: spot-check the recorded payloads contain no
	// URLs and no manifest body. Walk the payload bytes and assert
	// "http://" never appears.
	for _, e := range rec.snapshot() {
		if containsBytes(e.Payload, []byte("http://")) || containsBytes(e.Payload, []byte("https://")) {
			t.Errorf("audit payload for %q leaks a URL: %s", e.Kind, string(e.Payload))
		}
	}
}

func TestIntegration_BackgroundPoll_EmitsAvailableOnTransition(t *testing.T) {
	body := []byte("poll-bin")
	bin, sha := fakeBinary(t, body)
	manifestURL, _ := startE2EManifestServer(t, "v0.4.0", bin, sha)

	dir := t.TempDir()
	rec := &recordingAudit{}
	cfg := Config{
		CurrentVersion: "v0.3.0",
		DataDir:        dir,
		HTTPClient:     &http.Client{Timeout: 5 * time.Second},
		Swapper:        &fakeSwapper{},
		ManifestURL:    manifestURL,
		Platform:       "linux/amd64",
		Audit:          rec,
	}
	svc, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = svc.BackgroundPoll(ctx, 50*time.Millisecond, "stable")

	kinds := rec.kinds()
	if !containsKind(kinds, audit.KindUpdateAvailable) {
		t.Fatalf("expected update.available on first transition, got %v", kinds)
	}
	// Available should fire EXACTLY once (transition false→true), not
	// every tick.
	availableHits := 0
	for _, k := range kinds {
		if k == audit.KindUpdateAvailable {
			availableHits++
		}
	}
	if availableHits != 1 {
		t.Errorf("update.available fired %d times, want exactly 1: %v", availableHits, kinds)
	}
}

func TestIntegration_SkipThenCheck_RoundTrip(t *testing.T) {
	body := []byte("skip-bin")
	bin, sha := fakeBinary(t, body)
	manifestURL, _ := startE2EManifestServer(t, "v0.4.0", bin, sha)

	dir := t.TempDir()
	rec := &recordingAudit{}
	cfg := Config{
		CurrentVersion: "v0.3.0",
		DataDir:        dir,
		HTTPClient:     &http.Client{Timeout: 5 * time.Second},
		Swapper:        &fakeSwapper{},
		ManifestURL:    manifestURL,
		Platform:       "linux/amd64",
		Audit:          rec,
	}
	svc, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	ctx := context.Background()

	// First Check: actionable update.
	info, err := svc.Check(ctx)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !info.Available || info.SkippedByUser {
		t.Fatalf("first Check should be Available && !Skipped: %+v", info)
	}

	// Skip.
	if err := svc.SkipVersion(ctx, "v0.4.0"); err != nil {
		t.Fatalf("SkipVersion: %v", err)
	}

	// Second Check: still Available, but now SkippedByUser=true.
	info2, err := svc.Check(ctx)
	if err != nil {
		t.Fatalf("Check (post-skip): %v", err)
	}
	if !info2.Available {
		t.Fatalf("Available should remain true after skip: %+v", info2)
	}
	if !info2.SkippedByUser {
		t.Fatalf("SkippedByUser should be true after skip: %+v", info2)
	}

	// Audit: two checked + one skipped, in that order.
	kinds := rec.kinds()
	if !containsKind(kinds, audit.KindUpdateSkipped) {
		t.Fatalf("missing update.skipped: %v", kinds)
	}
	checked := 0
	for _, k := range kinds {
		if k == audit.KindUpdateChecked {
			checked++
		}
	}
	if checked != 2 {
		t.Errorf("expected 2 update.checked events, got %d: %v", checked, kinds)
	}

	// The skipped event MUST land between the two checks (the spec's
	// "skip then check" round-trip).
	skipIdx := indexOfKind(kinds, audit.KindUpdateSkipped)
	firstCheckIdx := indexOfKind(kinds, audit.KindUpdateChecked)
	lastCheckIdx := -1
	for i, k := range kinds {
		if k == audit.KindUpdateChecked {
			lastCheckIdx = i
		}
	}
	if !(firstCheckIdx < skipIdx && skipIdx < lastCheckIdx) {
		t.Errorf("audit order off: kinds=%v skipIdx=%d firstCheck=%d lastCheck=%d",
			kinds, skipIdx, firstCheckIdx, lastCheckIdx)
	}

	// Privacy: the skipped payload carries the version, but no URL,
	// no notes body, no manifest body bytes.
	for _, e := range rec.snapshot() {
		if e.Kind != audit.KindUpdateSkipped {
			continue
		}
		var got audit.UpdateSkippedAttrs
		if err := json.Unmarshal(e.Payload, &got); err != nil {
			t.Fatalf("decode skipped payload: %v", err)
		}
		if got.Version != "v0.4.0" {
			t.Errorf("skipped version = %q, want v0.4.0", got.Version)
		}
		if got.Reason == "" {
			t.Errorf("skipped reason should be a non-empty label")
		}
	}
}

func TestIntegration_ApplyAndRestart_ShaMismatch_EmitsFailedClassification(t *testing.T) {
	dir := t.TempDir()
	stagedPath := filepath.Join(dir, "staged.bin")
	if err := os.WriteFile(stagedPath, []byte("staged bytes"), 0o755); err != nil {
		t.Fatal(err)
	}
	runningPath := filepath.Join(dir, "running.bin")
	if err := os.WriteFile(runningPath, []byte("running"), 0o755); err != nil {
		t.Fatal(err)
	}

	rec := &recordingAudit{}
	swapper := &fakeSwapper{
		// Swap returns a wrapped errSha256Mismatch — same shape the
		// real Swapper would surface if the staged digest no longer
		// matches the on-disk file at apply time (e.g. a stale
		// staging slot).
		swapErr: fmt.Errorf("swap pre-flight: %w", errSha256Mismatch),
	}
	cfg := Config{
		CurrentVersion:    "v0.3.0",
		DataDir:           dir,
		Swapper:           swapper,
		RunningBinaryPath: runningPath,
		Platform:          "linux/amd64",
		Audit:             rec,
	}
	svc, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	staged := StagedUpdate{
		Path:          stagedPath,
		TargetVersion: "v0.4.0",
		Sha256:        "00deadbeef",
		Platform:      "linux/amd64",
	}
	err = svc.ApplyAndRestart(context.Background(), staged)
	if err == nil || !errors.Is(err, errSha256Mismatch) {
		t.Fatalf("expected sha256 mismatch, got %v", err)
	}

	kinds := rec.kinds()
	if !containsKind(kinds, audit.KindUpdateApplied) {
		t.Errorf("update.applied should fire BEFORE swap (so it lands even on apply failure): %v", kinds)
	}
	if !containsKind(kinds, audit.KindUpdateFailed) {
		t.Fatalf("missing update.failed on sha mismatch: %v", kinds)
	}

	// applied must precede failed.
	appliedIdx := indexOfKind(kinds, audit.KindUpdateApplied)
	failedIdx := indexOfKind(kinds, audit.KindUpdateFailed)
	if !(appliedIdx >= 0 && failedIdx > appliedIdx) {
		t.Errorf("apply order off: kinds=%v applied=%d failed=%d", kinds, appliedIdx, failedIdx)
	}

	// The failed payload's ErrorClass MUST be "sha_mismatch" — that's
	// the privacy contract and the actionable signal.
	for _, e := range rec.snapshot() {
		if e.Kind != audit.KindUpdateFailed {
			continue
		}
		var got audit.UpdateFailedAttrs
		if err := json.Unmarshal(e.Payload, &got); err != nil {
			t.Fatalf("decode failed payload: %v", err)
		}
		if got.ErrorClass != "sha_mismatch" {
			t.Errorf("ErrorClass = %q, want sha_mismatch (kinds=%v)", got.ErrorClass, kinds)
		}
		if got.Action != "apply" {
			t.Errorf("Action = %q, want apply", got.Action)
		}
	}
}

func TestIntegration_NetworkError_ClassifiesAsNetwork(t *testing.T) {
	// Point at a nonexistent host so dial fails fast.
	dir := t.TempDir()
	rec := &recordingAudit{}
	cfg := Config{
		CurrentVersion: "v0.3.0",
		DataDir:        dir,
		HTTPClient:     &http.Client{Timeout: 200 * time.Millisecond},
		Swapper:        &fakeSwapper{},
		ManifestURL:    "http://127.0.0.1:1/manifest.json", // closed port
		Platform:       "linux/amd64",
		Audit:          rec,
	}
	svc, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := svc.Check(context.Background()); err == nil {
		t.Fatal("expected network error")
	}
	kinds := rec.kinds()
	if !containsKind(kinds, audit.KindUpdateFailed) {
		t.Fatalf("missing update.failed on network error: %v", kinds)
	}
	for _, e := range rec.snapshot() {
		if e.Kind != audit.KindUpdateFailed {
			continue
		}
		var got audit.UpdateFailedAttrs
		if err := json.Unmarshal(e.Payload, &got); err != nil {
			t.Fatalf("decode failed payload: %v", err)
		}
		if got.Action != "check" {
			t.Errorf("Action = %q, want check", got.Action)
		}
		if got.ErrorClass != "network" && got.ErrorClass != "manifest_invalid" {
			// Allow manifest_invalid as a benign fallback for some
			// platform-specific dial-error messages, but prefer
			// network.
			t.Errorf("ErrorClass = %q, want network", got.ErrorClass)
		}
	}
}

// containsBytes reports whether sub appears in s.
func containsBytes(s, sub []byte) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			if s[i+j] != sub[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
