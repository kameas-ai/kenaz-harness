package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
)

// ── helpers ─────────────────────────────────────────────────────────

// fakeSwapper is the test Swapper. It records the calls without
// touching the running process — Restart MUST NOT os.Exit in tests.
type fakeSwapper struct {
	mu          sync.Mutex
	swapCalls   int
	restartCall string
	swapErr     error
	restartErr  error
}

func (f *fakeSwapper) Swap(_ context.Context, staged StagedUpdate, runningBinaryPath, dataDir string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.swapCalls++
	if f.swapErr != nil {
		return f.swapErr
	}
	// Mirror real behavior: validate inputs.
	if !fileExists(staged.Path) {
		return fmt.Errorf("staged path missing: %s", staged.Path)
	}
	if runningBinaryPath == "" {
		return errors.New("empty running binary path")
	}
	_ = dataDir
	return nil
}

func (f *fakeSwapper) Restart(_ context.Context, newBinaryPath string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restartCall = newBinaryPath
	return f.restartErr
}

func (f *fakeSwapper) snapshot() (int, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.swapCalls, f.restartCall
}

// recordingPublisher captures broker Publish calls for assertion.
type recordingPublisher struct {
	mu    sync.Mutex
	calls []publishCall
}

type publishCall struct {
	topic   string
	payload any
}

func (p *recordingPublisher) Publish(topic string, payload any) {
	p.mu.Lock()
	p.calls = append(p.calls, publishCall{topic, payload})
	p.mu.Unlock()
}

func (p *recordingPublisher) snapshot() []publishCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]publishCall, len(p.calls))
	copy(out, p.calls)
	return out
}

// fakeBinary is a tiny payload + its sha256.
func fakeBinary(t *testing.T, body []byte) (payload []byte, sha string) {
	t.Helper()
	h := sha256.Sum256(body)
	return body, hex.EncodeToString(h[:])
}

// startManifestServer spins up a httptest server that returns the
// supplied manifest at /manifest.json and serves the binary body at
// /bin. Returns the server URLs and a cleanup func.
func startManifestServer(t *testing.T, mPath string, mBody []byte, binBody []byte) (manifestURL, binURL string, srv *httptest.Server) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc(mPath, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(mBody)
	})
	mux.HandleFunc("/bin", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(binBody)))
		_, _ = w.Write(binBody)
	})
	srv = httptest.NewServer(mux)
	return srv.URL + mPath, srv.URL + "/bin", srv
}

// newTestService spins up a service with a tmp DataDir, fake swapper,
// and a publisher. Returns the service plus the test handles.
func newTestService(t *testing.T, currentVersion, manifestURL, platform string) (*service, *fakeSwapper, *recordingPublisher) {
	t.Helper()
	dataDir := t.TempDir()
	swapper := &fakeSwapper{}
	pub := &recordingPublisher{}
	cfg := Config{
		CurrentVersion: currentVersion,
		DataDir:        dataDir,
		HTTPClient:     &http.Client{Timeout: 5 * time.Second},
		Publisher:      pub,
		Swapper:        swapper,
		ManifestURL:    manifestURL,
		Platform:       platform,
	}
	svc, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc.(*service), swapper, pub
}

// ── tests ───────────────────────────────────────────────────────────

func TestNewService_RequiresFields(t *testing.T) {
	if _, err := NewService(Config{DataDir: "/tmp"}); err == nil {
		t.Fatal("expected error for missing CurrentVersion")
	}
	if _, err := NewService(Config{CurrentVersion: "v0.1.0"}); err == nil {
		t.Fatal("expected error for missing DataDir")
	}
}

func TestIsNewer(t *testing.T) {
	cases := []struct {
		current, available string
		want               bool
	}{
		{"v0.1.0", "v0.2.0", true},
		{"v0.2.0", "v0.2.0", false},
		{"v0.3.0", "v0.2.0", false},
		{"dev", "v9.9.9", false}, // dev never updates
		{"", "v0.1.0", false},
		{"v0.1.0", "", false},
		{"garbage", "v0.1.0", false},
		{"0.1.0", "0.2.0", true}, // tolerate missing leading v
	}
	for _, c := range cases {
		got := isNewer(c.current, c.available)
		if got != c.want {
			t.Errorf("isNewer(%q,%q) = %v, want %v", c.current, c.available, got, c.want)
		}
	}
}

func TestService_Check_HappyPath(t *testing.T) {
	bin, sha := fakeBinary(t, []byte("fake binary contents"))
	manifestBody, _ := json.Marshal(manifest{
		Version: "v0.4.0",
		Notes:   "rolled the dice",
		Assets: []manifestAsset{
			{Platform: "linux/amd64", URL: "REPLACED", Sha256: sha},
		},
	})
	manifestURL, binURL, srv := startManifestServer(t, "/manifest.json", manifestBody, bin)
	defer srv.Close()

	// Patch manifest body so URL points at the live test server.
	patched := []byte(replaceFirst(string(manifestBody), "REPLACED", binURL))
	manifestURL, _, srv2 := startManifestServer(t, "/manifest.json", patched, bin)
	defer srv2.Close()

	svc, _, _ := newTestService(t, "v0.3.0", manifestURL, "linux/amd64")
	info, err := svc.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if info.AvailableVersion != "v0.4.0" || !info.Available {
		t.Fatalf("info = %+v", info)
	}
	if info.DownloadURL == "" || info.Sha256 != sha {
		t.Fatalf("info missing asset fields: %+v", info)
	}
	if info.SkippedByUser {
		t.Fatal("fresh install should not be skipped")
	}
}

func TestService_Check_NotNewer(t *testing.T) {
	bin, sha := fakeBinary(t, []byte("x"))
	body, _ := json.Marshal(manifest{
		Version: "v0.3.0",
		Assets:  []manifestAsset{{Platform: "linux/amd64", URL: "u", Sha256: sha}},
	})
	manifestURL, _, srv := startManifestServer(t, "/manifest.json", body, bin)
	defer srv.Close()

	svc, _, _ := newTestService(t, "v0.3.0", manifestURL, "linux/amd64")
	info, err := svc.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if info.Available {
		t.Fatalf("equal versions should not be Available: %+v", info)
	}
}

func TestService_Check_NoAssetForPlatform(t *testing.T) {
	body, _ := json.Marshal(manifest{
		Version: "v0.4.0",
		Assets:  []manifestAsset{{Platform: "windows/amd64", URL: "u", Sha256: "abc"}},
	})
	manifestURL, _, srv := startManifestServer(t, "/manifest.json", body, []byte("x"))
	defer srv.Close()

	svc, _, _ := newTestService(t, "v0.3.0", manifestURL, "linux/amd64")
	info, err := svc.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if info.DownloadURL != "" || info.Sha256 != "" {
		t.Fatalf("expected empty asset fields, got %+v", info)
	}
	// Available may still be true (semver says so) — that's fine; the
	// frontend shows the banner with no install button.
	if !info.Available {
		t.Fatal("expected Available true on semver basis")
	}
}

func TestService_Download_VerifiesSha256(t *testing.T) {
	bin, sha := fakeBinary(t, []byte("payload bytes"))
	body, _ := json.Marshal(manifest{
		Version: "v0.4.0",
		Assets:  []manifestAsset{{Platform: "linux/amd64", URL: "REPLACED", Sha256: sha}},
	})
	manifestURL, binURL, srv := startManifestServer(t, "/manifest.json", body, bin)
	defer srv.Close()
	patched := []byte(replaceFirst(string(body), "REPLACED", binURL))
	manifestURL, _, srv2 := startManifestServer(t, "/manifest.json", patched, bin)
	defer srv2.Close()

	svc, _, _ := newTestService(t, "v0.3.0", manifestURL, "linux/amd64")
	info, err := svc.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	prog, staged, err := svc.Download(context.Background(), info)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	last := DownloadProgress{}
	for p := range prog {
		last = p
	}
	if !last.Done {
		t.Fatalf("last progress not Done: %+v", last)
	}
	if last.Err != nil {
		t.Fatalf("download err: %v", last.Err)
	}
	if !fileExists(staged.Path) {
		t.Fatalf("staged file missing at %s", staged.Path)
	}
	if staged.Sha256 != sha {
		t.Fatalf("staged sha mismatch")
	}
}

func TestService_Download_RejectsBadSha(t *testing.T) {
	bin := []byte("payload")
	wrongSha := "deadbeef" + "00000000000000000000000000000000000000000000000000000000"
	body, _ := json.Marshal(manifest{
		Version: "v0.4.0",
		Assets:  []manifestAsset{{Platform: "linux/amd64", URL: "REPLACED", Sha256: wrongSha}},
	})
	manifestURL, binURL, srv := startManifestServer(t, "/manifest.json", body, bin)
	defer srv.Close()
	patched := []byte(replaceFirst(string(body), "REPLACED", binURL))
	manifestURL, _, srv2 := startManifestServer(t, "/manifest.json", patched, bin)
	defer srv2.Close()

	svc, _, _ := newTestService(t, "v0.3.0", manifestURL, "linux/amd64")
	info, err := svc.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	prog, staged, err := svc.Download(context.Background(), info)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	last := DownloadProgress{}
	for p := range prog {
		last = p
	}
	if last.Err == nil || !errors.Is(last.Err, errSha256Mismatch) {
		t.Fatalf("expected sha mismatch, got: %v", last.Err)
	}
	if fileExists(staged.Path) {
		t.Fatalf("bad-sha staged file should be cleaned up: %s", staged.Path)
	}
}

func TestService_ApplyAndRestart_UsesSwapper(t *testing.T) {
	dir := t.TempDir()
	stagedPath := filepath.Join(dir, "kenaz-harness-v0.4.0.new")
	if err := os.WriteFile(stagedPath, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	runningPath := filepath.Join(dir, "kenaz-harness")
	if err := os.WriteFile(runningPath, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	swapper := &fakeSwapper{}
	cfg := Config{
		CurrentVersion:    "v0.3.0",
		DataDir:           dir,
		Swapper:           swapper,
		RunningBinaryPath: runningPath,
		Platform:          "linux/amd64",
	}
	svc, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	staged := StagedUpdate{
		Path:          stagedPath,
		TargetVersion: "v0.4.0",
		Sha256:        "deadbeef",
		Platform:      "linux/amd64",
	}
	if err := svc.ApplyAndRestart(context.Background(), staged); err != nil {
		t.Fatalf("ApplyAndRestart: %v", err)
	}
	swaps, restartTarget := swapper.snapshot()
	if swaps != 1 {
		t.Fatalf("expected 1 swap call, got %d", swaps)
	}
	if restartTarget != runningPath {
		t.Fatalf("Restart called with %q, want %q", restartTarget, runningPath)
	}
}

func TestService_ApplyAndRestart_PropagatesSwapError(t *testing.T) {
	dir := t.TempDir()
	swapper := &fakeSwapper{swapErr: errors.New("disk full")}
	cfg := Config{
		CurrentVersion:    "v0.3.0",
		DataDir:           dir,
		Swapper:           swapper,
		RunningBinaryPath: filepath.Join(dir, "running"),
	}
	svc, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	err = svc.ApplyAndRestart(context.Background(), StagedUpdate{
		Path:          filepath.Join(dir, "x"),
		TargetVersion: "v0.4.0",
		Sha256:        "abc",
	})
	if err == nil || err.Error() != "disk full" {
		t.Fatalf("expected disk full, got %v", err)
	}
	_, restartTarget := swapper.snapshot()
	if restartTarget != "" {
		t.Fatal("Restart should not be called after Swap error")
	}
}

func TestService_SkipVersion_PersistsAndAffectsCheck(t *testing.T) {
	bin, sha := fakeBinary(t, []byte("x"))
	body, _ := json.Marshal(manifest{
		Version: "v0.4.0",
		Assets:  []manifestAsset{{Platform: "linux/amd64", URL: "u", Sha256: sha}},
	})
	manifestURL, _, srv := startManifestServer(t, "/manifest.json", body, bin)
	defer srv.Close()

	svc, _, _ := newTestService(t, "v0.3.0", manifestURL, "linux/amd64")

	info, err := svc.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if info.SkippedByUser {
		t.Fatal("expected fresh install not skipped")
	}

	if err := svc.SkipVersion(context.Background(), "v0.4.0"); err != nil {
		t.Fatalf("SkipVersion: %v", err)
	}

	info, err = svc.Check(context.Background())
	if err != nil {
		t.Fatalf("Check (post-skip): %v", err)
	}
	if !info.SkippedByUser {
		t.Fatal("expected SkippedByUser=true after SkipVersion")
	}

	// New service over the same DataDir should reload the skip set.
	svc2, _, _ := newTestService(t, "v0.3.0", manifestURL, "linux/amd64")
	svc2.cfg.DataDir = svc.cfg.DataDir // reuse the same DataDir
	if err := svc2.loadSkipped(); err != nil {
		t.Fatalf("reload skip: %v", err)
	}
	info, err = svc2.Check(context.Background())
	if err != nil {
		t.Fatalf("Check (svc2): %v", err)
	}
	if !info.SkippedByUser {
		t.Fatal("skip should persist across restarts")
	}
}

func TestService_SkipVersion_RejectsEmpty(t *testing.T) {
	svc, _, _ := newTestService(t, "v0.3.0", "http://localhost/manifest.json", "linux/amd64")
	if err := svc.SkipVersion(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty version")
	}
}

func TestService_BackgroundPoll_PublishesOnAvailable(t *testing.T) {
	bin, sha := fakeBinary(t, []byte("x"))
	body, _ := json.Marshal(manifest{
		Version: "v0.4.0",
		Notes:   "ship it",
		Assets:  []manifestAsset{{Platform: "linux/amd64", URL: "u", Sha256: sha}},
	})
	manifestURL, _, srv := startManifestServer(t, "/manifest.json", body, bin)
	defer srv.Close()

	svc, _, pub := newTestService(t, "v0.3.0", manifestURL, "linux/amd64")

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = svc.BackgroundPoll(ctx, 50*time.Millisecond, "stable")

	calls := pub.snapshot()
	if len(calls) == 0 {
		t.Fatal("expected at least one Publish call")
	}
	if calls[0].topic != AvailableTopic {
		t.Fatalf("topic = %q want %q", calls[0].topic, AvailableTopic)
	}
	info, ok := calls[0].payload.(Info)
	if !ok {
		t.Fatalf("payload type = %T", calls[0].payload)
	}
	if info.AvailableVersion != "v0.4.0" {
		t.Fatalf("payload version = %q", info.AvailableVersion)
	}
}

func TestService_BackgroundPoll_SuppressesSkipped(t *testing.T) {
	bin, sha := fakeBinary(t, []byte("x"))
	body, _ := json.Marshal(manifest{
		Version: "v0.4.0",
		Assets:  []manifestAsset{{Platform: "linux/amd64", URL: "u", Sha256: sha}},
	})
	manifestURL, _, srv := startManifestServer(t, "/manifest.json", body, bin)
	defer srv.Close()

	svc, _, pub := newTestService(t, "v0.3.0", manifestURL, "linux/amd64")
	if err := svc.SkipVersion(context.Background(), "v0.4.0"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = svc.BackgroundPoll(ctx, 30*time.Millisecond, "stable")

	if calls := pub.snapshot(); len(calls) != 0 {
		t.Fatalf("expected no publishes for skipped version, got %d", len(calls))
	}
}

func TestService_BackgroundPoll_RequiresPositiveInterval(t *testing.T) {
	svc, _, _ := newTestService(t, "v0.3.0", "http://localhost/manifest.json", "linux/amd64")
	if err := svc.BackgroundPoll(context.Background(), 0, "stable"); err == nil {
		t.Fatal("expected error for zero interval")
	}
}

func TestService_PrereleaseFallback(t *testing.T) {
	bin, sha := fakeBinary(t, []byte("x"))
	stableBody, _ := json.Marshal(manifest{
		Version: "v0.4.0",
		Assets:  []manifestAsset{{Platform: "linux/amd64", URL: "u", Sha256: sha}},
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/downloads/manifest.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(stableBody)
	})
	mux.HandleFunc("/downloads/manifest-prerelease.json", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	_ = bin

	// Use ManifestURL = "" + override the resolution by pointing at
	// our test server through monkey-patching the channel URLs. We
	// validate the fallback by constructing a service that first
	// queries prerelease then falls back to stable.
	dataDir := t.TempDir()
	cfg := Config{
		CurrentVersion: "v0.3.0",
		DataDir:        dataDir,
		HTTPClient:     &http.Client{Timeout: 5 * time.Second},
		Platform:       "linux/amd64",
		Swapper:        &fakeSwapper{},
	}
	svc, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	s := svc.(*service)

	// Drive checkChannel with an override URL set per-call by
	// stubbing s.cfg.ManifestURL to the prerelease URL — the
	// fallback path keys on errManifestNotFound + channel ==
	// "prerelease" + ManifestURL == "" so we exercise the literal
	// production code path here.
	s.cfg.ManifestURL = "" // ensure prod path
	// Swap the production manifest URL constants via a one-off
	// HTTPClient that maps to our test server.
	s.client = &http.Client{
		Transport: &rewriteTransport{base: srv.URL},
		Timeout:   5 * time.Second,
	}
	info, err := s.checkChannel(context.Background(), "prerelease")
	if err != nil {
		t.Fatalf("checkChannel: %v", err)
	}
	if info.AvailableVersion != "v0.4.0" {
		t.Fatalf("expected fallback to stable manifest, got %+v", info)
	}
}

// rewriteTransport rewrites the host of every request to base. Lets
// the test target docs.kameas.ai URLs against a httptest server
// without monkey-patching package-level constants.
type rewriteTransport struct {
	base string
}

func (rt *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Replace scheme+host with rt.base; keep path.
	newURL := rt.base + req.URL.Path
	r2, err := http.NewRequestWithContext(req.Context(), req.Method, newURL, nil)
	if err != nil {
		return nil, err
	}
	r2.Header = req.Header.Clone()
	return http.DefaultTransport.RoundTrip(r2)
}

// replaceFirst is a tiny strings.Replace(.., 1) wrapper; avoids
// importing strings into the helper section repeatedly.
func replaceFirst(s, old, new string) string {
	for i := 0; i+len(old) <= len(s); i++ {
		if s[i:i+len(old)] == old {
			return s[:i] + new + s[i+len(old):]
		}
	}
	return s
}

func TestEqualFold(t *testing.T) {
	cases := []struct {
		a, b string
		eq   bool
	}{
		{"abc", "ABC", true},
		{"abc", "abd", false},
		{"abc", "ab", false},
		{"", "", true},
	}
	for _, c := range cases {
		if got := equalFold(c.a, c.b); got != c.eq {
			t.Errorf("equalFold(%q,%q) = %v want %v", c.a, c.b, got, c.eq)
		}
	}
}

func TestVerifyFileSha256(t *testing.T) {
	dir := t.TempDir()
	body := []byte("hello")
	h := sha256.Sum256(body)
	sha := hex.EncodeToString(h[:])
	p := filepath.Join(dir, "f")
	if err := os.WriteFile(p, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyFileSha256(p, sha); err != nil {
		t.Fatalf("expected match: %v", err)
	}
	if err := verifyFileSha256(p, "deadbeef"); !errors.Is(err, errSha256Mismatch) {
		t.Fatalf("expected mismatch error: %v", err)
	}
	if err := verifyFileSha256(filepath.Join(dir, "nope"), sha); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestPickAssetFor(t *testing.T) {
	m := manifest{
		Assets: []manifestAsset{
			{Platform: "darwin/arm64", URL: "a"},
			{Platform: "linux/amd64", URL: "b"},
		},
	}
	if a, ok := pickAssetFor(m, "linux/amd64"); !ok || a.URL != "b" {
		t.Fatalf("expected linux match: %+v", a)
	}
	if _, ok := pickAssetFor(m, "freebsd/amd64"); ok {
		t.Fatal("expected no match")
	}
}

func TestDefaultStagedName(t *testing.T) {
	if got := defaultStagedName("darwin/arm64", "v0.4.0"); got != "kenaz-harness-v0.4.0.app.new" {
		t.Errorf("darwin name = %q", got)
	}
	if got := defaultStagedName("windows/amd64", "v0.4.0"); got != "kenaz-harness-v0.4.0.exe.new" {
		t.Errorf("windows name = %q", got)
	}
	if got := defaultStagedName("linux/amd64", "v0.4.0"); got != "kenaz-harness-v0.4.0.new" {
		t.Errorf("linux name = %q", got)
	}
	if got := defaultStagedName("linux/amd64", ""); got != "kenaz-harness-next.new" {
		t.Errorf("empty version = %q", got)
	}
}
