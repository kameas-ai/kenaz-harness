package chat

// AC-PI-2 fixture audit (chat-turn-integrity-01PMZ606 WP03): this file's
// fake (fakeStreamCheckpointWriter, formerly fakePartialPersister) is the
// fixture spec.md §1.1.2 names as the one that hid the CHAT-01 P0 for
// five releases — it stood in for the production PartialPersister
// closure, so a test built on it never exercised the real
// AppendMessage-per-tick INSERT the fix removed.
//
// It is legitimate to keep a fake here, but only because what changed:
// StreamCheckpointWriter is a single passthrough method
// (*session.Manager.UpsertStreamCheckpoint), not a two-call closure with
// its own logic to hide behind a fake. The three tests below assert
// runPeriodicFlush's OWN control flow — the ticker interval, the
// skip-when-no-growth watermark, and ctx-cancel exit — none of which is
// a persistence property (test doctrine rule 3, spec.md §8: "never
// assert on a fake's return value where the real seam does the work").
// The persistence-shaped assertions (AC-001/AC-002/AC-004 — row counts
// and content against real sqlite) live in p0_repro_test.go, which
// drives the real *session.Manager end to end and is the file that
// actually proves the fix, not this one.

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
)

// fakeStreamCheckpointWriter records UpsertStreamCheckpoint calls for
// flush-loop control-flow tests. Race-safe per CLAUDE.md's mutex +
// snapshot pattern — CI runs -race with CGO_ENABLED=1 and this fake is
// written from the flush goroutine, read from the test body.
type fakeStreamCheckpointWriter struct {
	mu    sync.Mutex
	calls []checkpointCall
	// errFn can inject errors per-call; nil means success.
	errFn func(n int) error
}

type checkpointCall struct {
	sessionID string
	subID     string
	text      string
	hasTool   bool
}

func (f *fakeStreamCheckpointWriter) UpsertStreamCheckpoint(_ context.Context, sessionID, subID, text string, hasTool bool) error {
	f.mu.Lock()
	n := len(f.calls)
	f.calls = append(f.calls, checkpointCall{sessionID, subID, text, hasTool})
	errFn := f.errFn
	f.mu.Unlock()

	if errFn != nil {
		if err := errFn(n); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeStreamCheckpointWriter) snapshot() []checkpointCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]checkpointCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// streamCheckpointWriterFunc adapts a function value to
// StreamCheckpointWriter, mirroring PartialPersisterFunc's shape for
// the tests that only need a bare counter.
type streamCheckpointWriterFunc func(ctx context.Context, sessionID, subID, text string, hasTool bool) error

func (f streamCheckpointWriterFunc) UpsertStreamCheckpoint(ctx context.Context, sessionID, subID, text string, hasTool bool) error {
	return f(ctx, sessionID, subID, text, hasTool)
}

// TestRunPeriodicFlush_FlushesNewContent verifies that runPeriodicFlush
// calls UpsertStreamCheckpoint when new content has been accumulated
// since the last tick (FR-002 acceptance: mid-turn snapshot exists
// before turn end), and that it forwards PartialState's second return
// (hasTool) instead of the old hardcoded true (spec.md §1.1.3).
func TestRunPeriodicFlush_FlushesNewContent(t *testing.T) {
	t.Parallel()

	broker := &recordingBroker{}
	bridge := NewStreamBridge(broker, "sub-2", "sess-2")

	// Emit some text BEFORE the flush loop starts.
	bridge.Emit(coreag.StreamEvent{Kind: coreag.StreamEventText, Text: "hello world"})

	writer := &fakeStreamCheckpointWriter{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Use a very short interval for tests.
	interval := 50 * time.Millisecond

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		runPeriodicFlush(ctx, "sess-2", "sub-2", bridge, writer, interval)
	}()

	// Wait for at least one flush tick.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		calls := writer.snapshot()
		if len(calls) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	wg.Wait()

	calls := writer.snapshot()
	if len(calls) == 0 {
		t.Fatal("expected at least one UpsertStreamCheckpoint call, got 0")
	}
	if calls[0].sessionID != "sess-2" || calls[0].subID != "sub-2" {
		t.Errorf("sessionID/subID = %q/%q, want %q/%q", calls[0].sessionID, calls[0].subID, "sess-2", "sub-2")
	}
	if calls[0].text != "hello world" {
		t.Errorf("text = %q, want %q", calls[0].text, "hello world")
	}
	// No tool_use was emitted on this bridge, so PartialState's second
	// return is false — this pins that runPeriodicFlush now DERIVES
	// hasTool from the bridge instead of the old hardcoded
	// recoverable=true (spec.md §1.1.3, the second defect this WP
	// fixes).
	if calls[0].hasTool {
		t.Error("hasTool = true, want false (no tool_use was emitted on this bridge)")
	}
}

// TestRunPeriodicFlush_DerivesHasToolFromBridge verifies hasTool is
// forwarded true when the bridge recorded a tool call — the other half
// of §1.1.3's fix (the old code hardcoded true unconditionally, which
// this test would NOT have caught; it needs the false case above too).
func TestRunPeriodicFlush_DerivesHasToolFromBridge(t *testing.T) {
	t.Parallel()

	broker := &recordingBroker{}
	bridge := NewStreamBridge(broker, "sub-2b", "sess-2b")
	bridge.Emit(coreag.StreamEvent{Kind: coreag.StreamEventText, Text: "partial"})
	bridge.Emit(coreag.StreamEvent{Kind: coreag.StreamEventTool, ToolID: "call-1", ToolName: "kenaz__bash"})

	writer := &fakeStreamCheckpointWriter{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		runPeriodicFlush(ctx, "sess-2b", "sub-2b", bridge, writer, 30*time.Millisecond)
	}()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(writer.snapshot()) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	wg.Wait()

	calls := writer.snapshot()
	if len(calls) == 0 {
		t.Fatal("expected at least one UpsertStreamCheckpoint call, got 0")
	}
	if !calls[0].hasTool {
		t.Error("hasTool = false, want true (a tool_use was emitted on this bridge)")
	}
}

// TestRunPeriodicFlush_SkipsOnNoNewContent verifies that the flusher
// does NOT call UpsertStreamCheckpoint when no new text has been
// accumulated since the last flush (watermark logic).
func TestRunPeriodicFlush_SkipsOnNoNewContent(t *testing.T) {
	t.Parallel()

	broker := &recordingBroker{}
	bridge := NewStreamBridge(broker, "sub-3", "sess-3")
	// No text emitted; bridge stays empty.

	var callCount atomic.Int32
	writer := streamCheckpointWriterFunc(func(_ context.Context, _, _, _ string, _ bool) error {
		callCount.Add(1)
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		runPeriodicFlush(ctx, "sess-3", "sub-3", bridge, writer, 30*time.Millisecond)
	}()

	// Let the ticker fire 3 times without any content.
	time.Sleep(150 * time.Millisecond)
	cancel()
	wg.Wait()

	if n := callCount.Load(); n != 0 {
		t.Errorf("expected 0 UpsertStreamCheckpoint calls on empty bridge, got %d", n)
	}
}

// TestRunPeriodicFlush_ExitsOnContextCancel verifies the goroutine exits
// cleanly when the run context is cancelled.
func TestRunPeriodicFlush_ExitsOnContextCancel(t *testing.T) {
	t.Parallel()

	broker := &recordingBroker{}
	bridge := NewStreamBridge(broker, "sub-4", "sess-4")
	writer := &fakeStreamCheckpointWriter{}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		runPeriodicFlush(ctx, "sess-4", "sub-4", bridge, writer, 1*time.Second)
	}()

	cancel() // should cause immediate exit
	select {
	case <-done:
		// Good — goroutine exited.
	case <-time.After(500 * time.Millisecond):
		t.Error("runPeriodicFlush did not exit within 500ms of context cancel")
	}
}
