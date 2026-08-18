package update

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	coreupdate "github.com/kameas-ai/kenaz-harness/core/update"
)

// fakeService is the test double used across this file. It records
// the calls made against it and lets each test override the per-method
// behaviour without touching the network or the filesystem.
type fakeService struct {
	mu sync.Mutex

	checkCalls   int
	checkInfo    coreupdate.Info
	checkErr     error
	downloadCh   chan coreupdate.DownloadProgress
	downloadStg  coreupdate.StagedUpdate
	downloadErr  error
	applyCalls   int
	applyErr     error
	skipCalls    []string
	skipErr      error
	skipped      []string
	unskipCalls  []string
	unskipErr    error
	pollCalls    int
}

func (f *fakeService) Check(_ context.Context) (coreupdate.Info, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checkCalls++
	return f.checkInfo, f.checkErr
}

func (f *fakeService) Download(_ context.Context, _ coreupdate.Info) (<-chan coreupdate.DownloadProgress, coreupdate.StagedUpdate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.downloadErr != nil {
		return nil, coreupdate.StagedUpdate{}, f.downloadErr
	}
	return f.downloadCh, f.downloadStg, nil
}

func (f *fakeService) ApplyAndRestart(_ context.Context, _ coreupdate.StagedUpdate) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applyCalls++
	return f.applyErr
}

func (f *fakeService) SkipVersion(_ context.Context, version string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.skipCalls = append(f.skipCalls, version)
	return f.skipErr
}

func (f *fakeService) BackgroundPoll(_ context.Context, _ time.Duration, _ string) error {
	f.mu.Lock()
	f.pollCalls++
	f.mu.Unlock()
	return nil
}

func (f *fakeService) ListSkipped() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := append([]string(nil), f.skipped...)
	return out
}

func (f *fakeService) UnskipVersion(_ context.Context, version string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unskipCalls = append(f.unskipCalls, version)
	return f.unskipErr
}

// recordingPublisher captures published events for assertion.
type recordingPublisher struct {
	mu     sync.Mutex
	events []event
}

type event struct {
	topic   string
	payload any
}

func (p *recordingPublisher) Publish(topic string, payload any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, event{topic: topic, payload: payload})
}

func (p *recordingPublisher) snapshot() []event {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]event, len(p.events))
	copy(out, p.events)
	return out
}

func (p *recordingPublisher) topicCount(topic string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, e := range p.events {
		if e.topic == topic {
			n++
		}
	}
	return n
}

// ── Status / nil-service paths ─────────────────────────────────────

func TestStatus_NilService_ReturnsCurrentVersion(t *testing.T) {
	m := New(Config{CurrentVersion: "v0.4.0"})
	out, err := m.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if out.CurrentVersion != "v0.4.0" {
		t.Errorf("CurrentVersion = %q, want v0.4.0", out.CurrentVersion)
	}
	if out.DownloadState != stateIdle {
		t.Errorf("DownloadState = %q, want %q", out.DownloadState, stateIdle)
	}
	if out.Available {
		t.Errorf("Available = true, want false (no Check yet)")
	}
}

func TestStartCheck_NilService_ReturnsErrServiceUnavailable(t *testing.T) {
	m := New(Config{})
	err := m.StartCheck(context.Background())
	if !errors.Is(err, ErrServiceUnavailable) {
		t.Errorf("want ErrServiceUnavailable, got %v", err)
	}
}

func TestApply_NilService_ReturnsErrServiceUnavailable(t *testing.T) {
	m := New(Config{})
	err := m.Apply(context.Background())
	if !errors.Is(err, ErrServiceUnavailable) {
		t.Errorf("want ErrServiceUnavailable, got %v", err)
	}
}

// ── Happy paths ────────────────────────────────────────────────────

func TestStartCheck_PopulatesInMemoryMirror(t *testing.T) {
	svc := &fakeService{
		checkInfo: coreupdate.Info{
			CurrentVersion:   "v0.4.0",
			AvailableVersion: "v0.4.1",
			Channel:          "stable",
			Available:        true,
			DownloadURL:      "https://example/kenaz-0.4.1.tar.gz",
			Sha256:           "abc",
			Notes:            "https://docs.kameas.ai/release/0.4.1",
		},
	}
	m := New(Config{Service: svc, CurrentVersion: "v0.4.0"})

	if err := m.StartCheck(context.Background()); err != nil {
		t.Fatalf("StartCheck: %v", err)
	}
	out, _ := m.Status(context.Background())
	if !out.Available {
		t.Errorf("Available = false, want true")
	}
	if out.AvailableVersion != "v0.4.1" {
		t.Errorf("AvailableVersion = %q, want v0.4.1", out.AvailableVersion)
	}
	if out.Channel != "stable" {
		t.Errorf("Channel = %q, want stable", out.Channel)
	}
	if out.ReleaseURL == "" {
		t.Errorf("ReleaseURL: expected non-empty when Notes is a URL")
	}
	if out.LastCheckedAt == 0 {
		t.Errorf("LastCheckedAt = 0, want recent unix timestamp")
	}
}

func TestStartCheck_ServiceErrorPropagates(t *testing.T) {
	svc := &fakeService{checkErr: errors.New("boom")}
	m := New(Config{Service: svc})
	err := m.StartCheck(context.Background())
	if err == nil || err.Error() != "boom" {
		t.Errorf("StartCheck: want 'boom' error, got %v", err)
	}
}

func TestStartDownload_NoInfo_ReturnsErrNoUpdateAvailable(t *testing.T) {
	svc := &fakeService{}
	m := New(Config{Service: svc})
	err := m.StartDownload(context.Background())
	if !errors.Is(err, ErrNoUpdateAvailable) {
		t.Errorf("want ErrNoUpdateAvailable, got %v", err)
	}
}

func TestStartDownload_PublishesProgressAndComplete(t *testing.T) {
	ch := make(chan coreupdate.DownloadProgress, 4)
	svc := &fakeService{
		checkInfo: coreupdate.Info{
			CurrentVersion:   "v0.4.0",
			AvailableVersion: "v0.4.1",
			Available:        true,
			DownloadURL:      "https://example/asset",
			Sha256:           "abc",
		},
		downloadCh: ch,
		downloadStg: coreupdate.StagedUpdate{
			Path: "/tmp/k", TargetVersion: "v0.4.1", Sha256: "abc", Platform: "darwin/arm64",
		},
	}
	pub := &recordingPublisher{}
	m := New(Config{Service: svc, Publisher: pub})
	if err := m.StartCheck(context.Background()); err != nil {
		t.Fatalf("StartCheck: %v", err)
	}

	if err := m.StartDownload(context.Background()); err != nil {
		t.Fatalf("StartDownload: %v", err)
	}

	// Producer side: simulate the service emitting two progress ticks
	// + one terminal Done tick.
	ch <- coreupdate.DownloadProgress{Bytes: 50, Total: 100}
	ch <- coreupdate.DownloadProgress{Bytes: 80, Total: 100}
	ch <- coreupdate.DownloadProgress{Bytes: 100, Total: 100, Done: true}
	close(ch)

	// Wait for the drainProgress goroutine to settle.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := m.Status(context.Background())
		if out.DownloadState == stateStaged {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	out, _ := m.Status(context.Background())
	if out.DownloadState != stateStaged {
		t.Fatalf("DownloadState = %q, want %q", out.DownloadState, stateStaged)
	}
	if pub.topicCount(TopicDownloadProgress) < 2 {
		t.Errorf("progress events = %d, want >= 2", pub.topicCount(TopicDownloadProgress))
	}
	if pub.topicCount(TopicDownloadComplete) != 1 {
		t.Errorf("complete events = %d, want 1", pub.topicCount(TopicDownloadComplete))
	}
}

func TestStartDownload_FailureMarksFailed(t *testing.T) {
	ch := make(chan coreupdate.DownloadProgress, 1)
	svc := &fakeService{
		checkInfo: coreupdate.Info{
			AvailableVersion: "v0.4.1",
			Available:        true,
			DownloadURL:      "https://example/asset",
			Sha256:           "abc",
		},
		downloadCh: ch,
	}
	pub := &recordingPublisher{}
	m := New(Config{Service: svc, Publisher: pub})
	_ = m.StartCheck(context.Background())
	if err := m.StartDownload(context.Background()); err != nil {
		t.Fatalf("StartDownload: %v", err)
	}
	ch <- coreupdate.DownloadProgress{Done: true, Err: errors.New("sha mismatch")}
	close(ch)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := m.Status(context.Background())
		if out.DownloadState == stateFailed {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	out, _ := m.Status(context.Background())
	if out.DownloadState != stateFailed {
		t.Errorf("DownloadState = %q, want failed", out.DownloadState)
	}
	if pub.topicCount(TopicDownloadFailed) != 1 {
		t.Errorf("failed events = %d, want 1", pub.topicCount(TopicDownloadFailed))
	}
}

func TestApply_HappyPath(t *testing.T) {
	ch := make(chan coreupdate.DownloadProgress, 1)
	svc := &fakeService{
		checkInfo: coreupdate.Info{
			AvailableVersion: "v0.4.1", Available: true,
			DownloadURL: "https://example/asset", Sha256: "abc",
		},
		downloadCh: ch,
		downloadStg: coreupdate.StagedUpdate{
			Path: "/tmp/k", TargetVersion: "v0.4.1", Sha256: "abc", Platform: "linux/amd64",
		},
	}
	m := New(Config{Service: svc})
	_ = m.StartCheck(context.Background())
	_ = m.StartDownload(context.Background())
	ch <- coreupdate.DownloadProgress{Done: true, Bytes: 1, Total: 1}
	close(ch)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := m.Status(context.Background())
		if out.DownloadState == stateStaged {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := m.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if svc.applyCalls != 1 {
		t.Errorf("applyCalls = %d, want 1", svc.applyCalls)
	}
}

func TestApply_NothingStaged(t *testing.T) {
	m := New(Config{Service: &fakeService{}})
	err := m.Apply(context.Background())
	if !errors.Is(err, ErrNothingStaged) {
		t.Errorf("want ErrNothingStaged, got %v", err)
	}
}

func TestSkipVersion_FlipsMirror(t *testing.T) {
	svc := &fakeService{
		checkInfo: coreupdate.Info{
			AvailableVersion: "v0.4.1", Available: true,
		},
	}
	m := New(Config{Service: svc})
	_ = m.StartCheck(context.Background())
	if err := m.SkipVersion(context.Background(), "v0.4.1"); err != nil {
		t.Fatalf("SkipVersion: %v", err)
	}
	out, _ := m.Status(context.Background())
	if !out.SkippedByUser {
		t.Errorf("SkippedByUser = false, want true")
	}
	if out.Available {
		t.Errorf("Available = true after Skip, want false")
	}
	if len(svc.skipCalls) != 1 || svc.skipCalls[0] != "v0.4.1" {
		t.Errorf("skipCalls = %v, want [v0.4.1]", svc.skipCalls)
	}
}

func TestListSkippedVersions_ReadsService(t *testing.T) {
	svc := &fakeService{skipped: []string{"v0.3.9", "v0.4.0"}}
	m := New(Config{Service: svc})
	got, err := m.ListSkippedVersions(context.Background())
	if err != nil {
		t.Fatalf("ListSkippedVersions: %v", err)
	}
	if len(got) != 2 || got[0] != "v0.3.9" || got[1] != "v0.4.0" {
		t.Errorf("ListSkippedVersions = %v, want [v0.3.9 v0.4.0]", got)
	}
}

func TestUnskipVersion_FlipsMirror(t *testing.T) {
	svc := &fakeService{
		checkInfo: coreupdate.Info{
			AvailableVersion: "v0.4.1", Available: true, SkippedByUser: true,
		},
	}
	m := New(Config{Service: svc})
	_ = m.StartCheck(context.Background())
	if err := m.UnskipVersion(context.Background(), "v0.4.1"); err != nil {
		t.Fatalf("UnskipVersion: %v", err)
	}
	out, _ := m.Status(context.Background())
	if out.SkippedByUser {
		t.Errorf("SkippedByUser = true after Unskip")
	}
	if len(svc.unskipCalls) != 1 {
		t.Errorf("unskipCalls = %v, want one entry", svc.unskipCalls)
	}
}

// ── Concurrency: parallel Status reads while a download mutates. ───

func TestStatus_ParallelReadsDuringDownload(t *testing.T) {
	ch := make(chan coreupdate.DownloadProgress, 64)
	svc := &fakeService{
		checkInfo: coreupdate.Info{
			AvailableVersion: "v0.4.1", Available: true,
			DownloadURL: "https://example/asset", Sha256: "abc",
		},
		downloadCh:  ch,
		downloadStg: coreupdate.StagedUpdate{TargetVersion: "v0.4.1"},
	}
	m := New(Config{Service: svc})
	_ = m.StartCheck(context.Background())
	if err := m.StartDownload(context.Background()); err != nil {
		t.Fatalf("StartDownload: %v", err)
	}

	// Producer: stream 200 progress ticks concurrently with readers.
	go func() {
		for i := int64(1); i <= 200; i++ {
			ch <- coreupdate.DownloadProgress{Bytes: i, Total: 200}
		}
		ch <- coreupdate.DownloadProgress{Bytes: 200, Total: 200, Done: true}
		close(ch)
	}()

	// Readers: spin a handful of goroutines hammering Status. The race
	// detector (-race) catches any unguarded shared-state access.
	var wg sync.WaitGroup
	var reads int64
	stop := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_, _ = m.Status(context.Background())
				atomic.AddInt64(&reads, 1)
			}
		}()
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := m.Status(context.Background())
		if out.DownloadState == stateStaged {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(stop)
	wg.Wait()

	if atomic.LoadInt64(&reads) < 100 {
		t.Errorf("status reads = %d, want >= 100 (mutex starvation?)", reads)
	}
	out, _ := m.Status(context.Background())
	if out.DownloadState != stateStaged {
		t.Errorf("final DownloadState = %q, want staged", out.DownloadState)
	}
}

// ── WP01: DownloadError wire-through + orphan-sweep guard ────────────────
//
// self-update-repair-01PMUP01. These tests pin the mutations named in
// tasks.md WP01:
//   - Status stops copying downloadErr → TestStatus_ReportsDownloadError fails.
//   - StartDownload drops the downloadErr clear → TestStartDownload_ClearsPreviousFailureReason fails.
// The orphan-sweep mutations (delete the sweep / widen the glob) are pinned
// in core/update/service_test.go since sweepOrphanPartFiles lives there.

func TestStatus_ReportsDownloadError(t *testing.T) {
	ch := make(chan coreupdate.DownloadProgress, 1)
	svc := &fakeService{
		checkInfo: coreupdate.Info{
			AvailableVersion: "v0.4.1", Available: true,
			DownloadURL: "https://example/asset", Sha256: "abc",
		},
		downloadCh: ch,
	}
	m := New(Config{Service: svc})
	_ = m.StartCheck(context.Background())
	if err := m.StartDownload(context.Background()); err != nil {
		t.Fatalf("StartDownload: %v", err)
	}
	ch <- coreupdate.DownloadProgress{Done: true, Err: errors.New("network reset")}
	close(ch)

	deadline := time.Now().Add(2 * time.Second)
	var out StatusOutput
	for time.Now().Before(deadline) {
		out, _ = m.Status(context.Background())
		if out.DownloadState == stateFailed {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if out.DownloadState != stateFailed {
		t.Fatalf("DownloadState = %q, want failed", out.DownloadState)
	}
	if out.DownloadError != "network reset" {
		t.Errorf("DownloadError = %q, want %q", out.DownloadError, "network reset")
	}
}

// TestStartDownload_ClearsPreviousFailureReason pins FR-004/FR-005's
// "the reason survives a reload" claim against a stale-error regression:
// a NEW StartDownload attempt must clear the PREVIOUS attempt's
// DownloadError so a fresh download-in-progress read never shows a stale
// failure string from a prior, unrelated attempt.
func TestStartDownload_ClearsPreviousFailureReason(t *testing.T) {
	ch1 := make(chan coreupdate.DownloadProgress, 1)
	svc := &fakeService{
		checkInfo: coreupdate.Info{
			AvailableVersion: "v0.4.1", Available: true,
			DownloadURL: "https://example/asset", Sha256: "abc",
		},
		downloadCh: ch1,
	}
	m := New(Config{Service: svc})
	_ = m.StartCheck(context.Background())
	if err := m.StartDownload(context.Background()); err != nil {
		t.Fatalf("StartDownload #1: %v", err)
	}
	ch1 <- coreupdate.DownloadProgress{Done: true, Err: errors.New("first attempt failed")}
	close(ch1)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := m.Status(context.Background())
		if out.DownloadState == stateFailed {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	failedOut, _ := m.Status(context.Background())
	if failedOut.DownloadError == "" {
		t.Fatalf("setup: first attempt did not record a DownloadError")
	}

	// Second attempt: a fresh channel, no tick sent yet. StartDownload
	// must clear the stale error immediately (synchronously, before the
	// goroutine even runs) so a Status read taken right after StartDownload
	// returns never sees the first attempt's reason.
	ch2 := make(chan coreupdate.DownloadProgress, 1)
	svc.mu.Lock()
	svc.downloadCh = ch2
	svc.mu.Unlock()
	if err := m.StartDownload(context.Background()); err != nil {
		t.Fatalf("StartDownload #2: %v", err)
	}
	mid, _ := m.Status(context.Background())
	if mid.DownloadError != "" {
		t.Errorf("DownloadError after second StartDownload = %q, want empty (stale reason survived)", mid.DownloadError)
	}
	close(ch2)
}
