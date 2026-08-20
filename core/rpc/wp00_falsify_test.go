package rpc

// wp00_falsify_test.go — controls-and-readouts-that-tell-the-truth-01PMZ808
// UNIT-0 / WP00. Observes (does not predict) whether the update poller
// honours the persisted AutoCheckUpdates=false setting. Deleted once WP07
// lands its own coverage (AC-014 supersedes this).

import (
	"context"
	"time"

	"github.com/kameas-ai/kenaz-harness/core"
	coreupdate "github.com/kameas-ai/kenaz-harness/core/update"
)

import "testing"

// fakeUpdateSvcWP00 is a minimal coreupdate.Service whose BackgroundPoll
// records that it was entered, then returns immediately.
type fakeUpdateSvcWP00 struct {
	pollEntered chan struct{}
}

func (f *fakeUpdateSvcWP00) Check(ctx context.Context) (coreupdate.Info, error) {
	return coreupdate.Info{}, nil
}
func (f *fakeUpdateSvcWP00) Download(ctx context.Context, info coreupdate.Info) (<-chan coreupdate.DownloadProgress, coreupdate.StagedUpdate, error) {
	ch := make(chan coreupdate.DownloadProgress)
	close(ch)
	return ch, coreupdate.StagedUpdate{}, nil
}
func (f *fakeUpdateSvcWP00) ApplyAndRestart(ctx context.Context, staged coreupdate.StagedUpdate) error {
	return nil
}
func (f *fakeUpdateSvcWP00) SkipVersion(ctx context.Context, version string) error { return nil }
func (f *fakeUpdateSvcWP00) BackgroundPoll(ctx context.Context, interval time.Duration, channel string) error {
	close(f.pollEntered)
	<-ctx.Done()
	return ctx.Err()
}

// TestWP00Falsify_UpdatePollerIgnoresAutoCheckUpdates observes claim 2 of
// tasks.md UNIT-0/WP00: with AutoCheckUpdates persisted false, does
// SetContext still enter BackgroundPoll? Per spec this determines whether
// UNIT-4 (WP07) is a real P1.
func TestWP00Falsify_UpdatePollerIgnoresAutoCheckUpdates(t *testing.T) {
	store := newTestStore(t)
	if err := store.SaveAutoCheckUpdates(false); err != nil {
		t.Fatalf("SaveAutoCheckUpdates(false): %v", err)
	}
	got, err := store.LoadAutoCheckUpdates()
	if err != nil || got != false {
		t.Fatalf("precondition failed: LoadAutoCheckUpdates() = %v, %v; want false, nil", got, err)
	}

	c, err := core.New(core.Options{DataDir: t.TempDir(), BuildVersion: "v0.0.0-wp00"})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := New(c, WithSettingsStore(store))

	fake := &fakeUpdateSvcWP00{pollEntered: make(chan struct{})}
	api.updateSvc = fake // override the real service with our spy

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	api.SetContext(ctx)

	select {
	case <-fake.pollEntered:
		t.Logf("OBSERVED: BackgroundPoll WAS entered despite AutoCheckUpdates=false persisted — CLAIM 2 CONFIRMED (poller ignores the setting).")
	case <-time.After(2 * time.Second):
		t.Fatalf("BackgroundPoll was never entered within 2s — claim 2 does NOT reproduce; UNIT-4's P1 status must be re-examined")
	}
}
