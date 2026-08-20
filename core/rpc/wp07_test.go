package rpc

// wp07_test.go — controls-and-readouts-that-tell-the-truth-01PMZ808 UNIT-4 /
// WP07. AC-014..AC-017: the three update dials (AutoCheckUpdates,
// UpdateChannel, UpdateCheckInterval) reach ReconfigureUpdatePoll /
// BackgroundPoll.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core"
	coreupdate "github.com/kameas-ai/kenaz-harness/core/update"
)

// wp07FakeUpdateSvc records every BackgroundPoll invocation
// (interval, channel) and whether it was entered at all. Race-safe per
// CLAUDE.md: writes happen on the goroutine BackgroundPoll runs on, reads
// happen from the test body, so every field is behind mu + a snapshot
// helper.
type wp07FakeUpdateSvc struct {
	mu    sync.Mutex
	calls []wp07PollCall
}

type wp07PollCall struct {
	interval time.Duration
	channel  string
}

func (f *wp07FakeUpdateSvc) Check(ctx context.Context) (coreupdate.Info, error) {
	return coreupdate.Info{}, nil
}
func (f *wp07FakeUpdateSvc) Download(ctx context.Context, info coreupdate.Info) (<-chan coreupdate.DownloadProgress, coreupdate.StagedUpdate, error) {
	ch := make(chan coreupdate.DownloadProgress)
	close(ch)
	return ch, coreupdate.StagedUpdate{}, nil
}
func (f *wp07FakeUpdateSvc) ApplyAndRestart(ctx context.Context, staged coreupdate.StagedUpdate) error {
	return nil
}
func (f *wp07FakeUpdateSvc) SkipVersion(ctx context.Context, version string) error { return nil }
func (f *wp07FakeUpdateSvc) BackgroundPoll(ctx context.Context, interval time.Duration, channel string) error {
	f.mu.Lock()
	f.calls = append(f.calls, wp07PollCall{interval: interval, channel: channel})
	f.mu.Unlock()
	<-ctx.Done()
	return ctx.Err()
}

func (f *wp07FakeUpdateSvc) snapshot() []wp07PollCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]wp07PollCall, len(f.calls))
	copy(out, f.calls)
	return out
}

func newWP07TestAPI(t *testing.T) (*API, *wp07FakeUpdateSvc) {
	t.Helper()
	store := newTestStore(t)
	c, err := core.New(core.Options{DataDir: t.TempDir(), BuildVersion: "v0.0.0-wp07"})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := New(c, WithSettingsStore(store))
	fake := &wp07FakeUpdateSvc{}
	api.updateSvc = fake
	return api, fake
}

// TestWP07_AC014_AutoCheckUpdatesFalse_NeverEntersBackgroundPoll.
// Mutation: restore the ungated `if a.updateSvc != nil { ...6h..."stable" }`
// block. Must fail (BackgroundPoll would be entered).
func TestWP07_AC014_AutoCheckUpdatesFalse_NeverEntersBackgroundPoll(t *testing.T) {
	api, fake := newWP07TestAPI(t)
	if err := api.settingsImpl.Store().SaveAutoCheckUpdates(false); err != nil {
		t.Fatalf("SaveAutoCheckUpdates(false): %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	api.ReconfigureUpdatePoll(ctx)

	// Give any (wrongly) launched goroutine a moment to record a call.
	time.Sleep(100 * time.Millisecond)
	if calls := fake.snapshot(); len(calls) != 0 {
		t.Fatalf("expected BackgroundPoll to never be entered with AutoCheckUpdates=false, got %d call(s): %+v", len(calls), calls)
	}
}

// TestWP07_AC015_UpdateCheckIntervalReachesBackgroundPoll.
func TestWP07_AC015_UpdateCheckIntervalReachesBackgroundPoll(t *testing.T) {
	api, fake := newWP07TestAPI(t)
	if err := api.settingsImpl.Store().SaveUpdateCheckInterval(1 * time.Hour); err != nil {
		t.Fatalf("SaveUpdateCheckInterval: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	api.ReconfigureUpdatePoll(ctx)

	waitForCalls(t, fake, 1)
	got := fake.snapshot()[0]
	if got.interval != 1*time.Hour {
		t.Fatalf("BackgroundPoll interval = %v, want 1h (not the old hardcoded 6h)", got.interval)
	}
}

// TestWP07_AC016_UpdateChannelReachesBackgroundPoll.
func TestWP07_AC016_UpdateChannelReachesBackgroundPoll(t *testing.T) {
	api, fake := newWP07TestAPI(t)
	if err := api.settingsImpl.Store().SaveUpdateChannel("prerelease"); err != nil {
		t.Fatalf("SaveUpdateChannel: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	api.ReconfigureUpdatePoll(ctx)

	waitForCalls(t, fake, 1)
	got := fake.snapshot()[0]
	if got.channel != "prerelease" {
		t.Fatalf("BackgroundPoll channel = %q, want %q", got.channel, "prerelease")
	}
}

// TestWP07_AC017_LiveSettingsSaveCancelsAndRelaunches asserts the
// Bindings.Settings_Set path: saving a new interval while the app is
// running cancels the old poller goroutine and starts a new one with the
// new value. Asserted by call count (a fresh BackgroundPoll entry) rather
// than sleeping on a timer.
func TestWP07_AC017_LiveSettingsSaveCancelsAndRelaunches(t *testing.T) {
	api, fake := newWP07TestAPI(t)
	bindings := NewBindings(api)
	bindings.SetSettingsStore(api.settingsImpl.Store())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bindings.SetContext(ctx) // captures b.appCtx, used by Settings_Set below
	api.ReconfigureUpdatePoll(ctx) // boot-time launch, mirrors SetContext

	waitForCalls(t, fake, 1)
	if got := fake.snapshot()[0].interval; got != 6*time.Hour {
		t.Fatalf("initial interval = %v, want the default 6h", got)
	}

	current, err := api.Settings().Get(context.Background())
	if err != nil {
		t.Fatalf("Settings().Get: %v", err)
	}
	current.UpdateCheckIntervalSec = 3600
	if err := bindings.Settings_Set(current); err != nil {
		t.Fatalf("Settings_Set: %v", err)
	}

	waitForCalls(t, fake, 2)
	calls := fake.snapshot()
	if calls[1].interval != 1*time.Hour {
		t.Fatalf("post-save interval = %v, want 1h — a live save must cancel and relaunch, not leave the old goroutine running", calls[1].interval)
	}
}

func waitForCalls(t *testing.T, fake *wp07FakeUpdateSvc, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(fake.snapshot()) >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d BackgroundPoll call(s); got %d: %+v", n, len(fake.snapshot()), fake.snapshot())
}
