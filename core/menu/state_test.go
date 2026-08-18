package menu

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	coreupdate "github.com/kameas-ai/kenaz-harness/core/update"

	updateview "github.com/kameas-ai/kenaz-harness/core/rpc/views/update"
)

func TestUpdateTopicState_MapsKnownTopics(t *testing.T) {
	cases := []struct {
		topic string
		want  UpdateMenuState
	}{
		{"update:available", UpdateAvailable},
		{"update:download-progress", UpdateDownloading},
		{"update:download-complete", UpdateStaged},
		{"update:download-failed", UpdateFailed},
	}
	for _, c := range cases {
		got, ok := UpdateTopicState(c.topic)
		if !ok {
			t.Errorf("UpdateTopicState(%q): ok=false, want true", c.topic)
		}
		if got != c.want {
			t.Errorf("UpdateTopicState(%q) = %v, want %v", c.topic, got, c.want)
		}
	}
}

func TestUpdateTopicState_UnknownTopicIsRejected(t *testing.T) {
	if _, ok := UpdateTopicState("menu:theme:set"); ok {
		t.Error("expected ok=false for an unrelated topic")
	}
}

// TestUpdateTopicState_MatchesProductionTopics pins the literal topic
// strings in updateTopicState against the actual constants
// core/rpc/views/update publishes on. Mutation: rename/typo one of the
// map keys in state.go → this test fails (drift between the menu
// package's independent literal and the real publisher).
func TestUpdateTopicState_MatchesProductionTopics(t *testing.T) {
	for _, topic := range []string{
		updateview.TopicDownloadProgress,
		updateview.TopicDownloadComplete,
		updateview.TopicDownloadFailed,
	} {
		if _, ok := UpdateTopicState(topic); !ok {
			t.Errorf("UpdateTopicState(%q) (from updateview package constant) = ok false", topic)
		}
	}
	if _, ok := UpdateTopicState(coreupdate.AvailableTopic); !ok {
		t.Errorf("UpdateTopicState(%q) (coreupdate.AvailableTopic) = ok false", coreupdate.AvailableTopic)
	}
}

// ── AC-8: drive the REAL Manager through idle → available → downloading
// → staged → failed, and derive MenuState.UpdateState purely from the
// topics it actually publishes (via UpdateTopicState) — not hand-set.
// This mirrors exactly what main.go's onUpdateTopic closures do.

// recordingMenuPublisher captures Publish calls and lets the test feed
// each topic through UpdateTopicState, mimicking main.go's
// wailsruntime.EventsOn callback bodies without needing a live Wails
// context (which a Go test cannot construct).
type recordingMenuPublisher struct {
	mu     sync.Mutex
	topics []string
}

func (p *recordingMenuPublisher) Publish(topic string, _ any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.topics = append(p.topics, topic)
}

func (p *recordingMenuPublisher) snapshot() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.topics))
	copy(out, p.topics)
	return out
}

// fakeUpdateService is the minimal coreupdate.Service double needed to
// drive Manager.StartDownload through a controlled progress channel.
type fakeUpdateService struct {
	info       coreupdate.Info
	downloadCh chan coreupdate.DownloadProgress
	staged     coreupdate.StagedUpdate
}

func (f *fakeUpdateService) Check(_ context.Context) (coreupdate.Info, error) {
	return f.info, nil
}
func (f *fakeUpdateService) Download(_ context.Context, _ coreupdate.Info) (<-chan coreupdate.DownloadProgress, coreupdate.StagedUpdate, error) {
	return f.downloadCh, f.staged, nil
}
func (f *fakeUpdateService) ApplyAndRestart(_ context.Context, _ coreupdate.StagedUpdate) error {
	return nil
}
func (f *fakeUpdateService) SkipVersion(_ context.Context, _ string) error { return nil }
func (f *fakeUpdateService) BackgroundPoll(_ context.Context, _ time.Duration, _ string) error {
	return nil
}

// applyPublishedTopics replays every topic recorded on pub through
// UpdateTopicState into a running MenuState.UpdateState, exactly as
// main.go's onUpdateTopic closures do — this IS the subscriber under
// test, not a hand-set value.
func applyPublishedTopics(topics []string) UpdateMenuState {
	state := UpdateIdle
	for _, topic := range topics {
		if s, ok := UpdateTopicState(topic); ok {
			state = s
		}
	}
	return state
}

func TestAC8_SubscriberDrivesAllFiveLabelsFromRealManagerPublishes_Staged(t *testing.T) {
	ch := make(chan coreupdate.DownloadProgress, 4)
	svc := &fakeUpdateService{
		info: coreupdate.Info{
			AvailableVersion: "v9.9.9",
			Available:        true,
			DownloadURL:      "https://example/asset",
			Sha256:           "abc",
		},
		downloadCh: ch,
		staged:     coreupdate.StagedUpdate{Path: "/tmp/x", TargetVersion: "v9.9.9"},
	}
	pub := &recordingMenuPublisher{}
	mgr := updateview.New(updateview.Config{Service: svc, Publisher: pub})
	ctx := context.Background()

	// idle: nothing published yet.
	if got := applyPublishedTopics(pub.snapshot()); got != UpdateIdle {
		t.Fatalf("idle: got %v, want UpdateIdle", got)
	}

	// available: StartCheck doesn't publish via this Manager (that's
	// core/update.Service.BackgroundPoll's job) — simulate the frontend
	// wiring's existing "update:available" publish directly, the same
	// way BackgroundPoll does in production.
	pub.Publish(coreupdate.AvailableTopic, svc.info)
	if got := applyPublishedTopics(pub.snapshot()); got != UpdateAvailable {
		t.Fatalf("available: got %v, want UpdateAvailable (label %q)", got, UpdateMenuLabel(got))
	}

	if err := mgr.StartCheck(ctx); err != nil {
		t.Fatalf("StartCheck: %v", err)
	}
	if err := mgr.StartDownload(ctx); err != nil {
		t.Fatalf("StartDownload: %v", err)
	}
	ch <- coreupdate.DownloadProgress{Bytes: 10, Total: 100}
	// Give drainProgress a moment to publish the progress tick before we
	// snapshot — StartDownload's own publish happens synchronously
	// relative to Status but progress ticks are async.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(pub.snapshot()) >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := applyPublishedTopics(pub.snapshot()); got != UpdateDownloading {
		t.Fatalf("downloading: got %v, want UpdateDownloading (label %q)", got, UpdateMenuLabel(got))
	}

	ch <- coreupdate.DownloadProgress{Done: true, Bytes: 100, Total: 100}
	close(ch)
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		st, _ := mgr.Status(ctx)
		if st.DownloadState == "staged" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	got := applyPublishedTopics(pub.snapshot())
	if got != UpdateStaged {
		t.Fatalf("staged: got %v, want UpdateStaged (label %q)", got, UpdateMenuLabel(got))
	}
	if label := UpdateMenuLabel(got); label != "Install & Restart" {
		t.Errorf("label = %q, want %q", label, "Install & Restart")
	}
}

func TestAC8_SubscriberDrivesFailedLabel(t *testing.T) {
	ch := make(chan coreupdate.DownloadProgress, 1)
	svc := &fakeUpdateService{
		info: coreupdate.Info{
			AvailableVersion: "v9.9.9",
			Available:        true,
			DownloadURL:      "https://example/asset",
			Sha256:           "abc",
		},
		downloadCh: ch,
	}
	pub := &recordingMenuPublisher{}
	mgr := updateview.New(updateview.Config{Service: svc, Publisher: pub})
	ctx := context.Background()

	if err := mgr.StartCheck(ctx); err != nil {
		t.Fatalf("StartCheck: %v", err)
	}
	if err := mgr.StartDownload(ctx); err != nil {
		t.Fatalf("StartDownload: %v", err)
	}
	ch <- coreupdate.DownloadProgress{Done: true, Err: errors.New("network reset")}
	close(ch)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		st, _ := mgr.Status(ctx)
		if st.DownloadState == "failed" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	got := applyPublishedTopics(pub.snapshot())
	if got != UpdateFailed {
		t.Fatalf("failed: got %v, want UpdateFailed (label %q)", got, UpdateMenuLabel(got))
	}
	if label := UpdateMenuLabel(got); label != "Retry Update" {
		t.Errorf("label = %q, want %q", label, "Retry Update")
	}
}
