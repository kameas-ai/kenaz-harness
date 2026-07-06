package chat

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
)

// fakePartialPersister records PersistPartial calls for flush tests.
type fakePartialPersister struct {
	mu    sync.Mutex
	calls []persistCall
	// errFn can inject errors per-call; nil means success.
	errFn func(n int) error
}

type persistCall struct {
	sessionID   string
	partialText string
	kind        string
	recoverable bool
}

func (f *fakePartialPersister) PersistPartial(_ context.Context, sessionID, partialText, kind string, recoverable bool) (string, error) {
	f.mu.Lock()
	n := len(f.calls)
	f.calls = append(f.calls, persistCall{sessionID, partialText, kind, recoverable})
	errFn := f.errFn
	f.mu.Unlock()

	if errFn != nil {
		if err := errFn(n); err != nil {
			return "", err
		}
	}
	return "flush-id", nil
}

func (f *fakePartialPersister) snapshot() []persistCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]persistCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// TestRunPeriodicFlush_FlushesNewContent verifies that runPeriodicFlush
// calls PersistPartial when new content has been accumulated since the last
// tick (FR-002 acceptance: mid-turn snapshot exists before turn end).
func TestRunPeriodicFlush_FlushesNewContent(t *testing.T) {
	t.Parallel()

	broker := &recordingBroker{}
	bridge := NewStreamBridge(broker, "sub-2", "sess-2")

	// Emit some text BEFORE the flush loop starts.
	bridge.Emit(coreag.StreamEvent{Kind: coreag.StreamEventText, Text: "hello world"})

	persister := &fakePartialPersister{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Use a very short interval for tests.
	interval := 50 * time.Millisecond

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		runPeriodicFlush(ctx, "sess-2", bridge, persister, interval)
	}()

	// Wait for at least one flush tick.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		calls := persister.snapshot()
		if len(calls) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	wg.Wait()

	calls := persister.snapshot()
	if len(calls) == 0 {
		t.Fatal("expected at least one PersistPartial call, got 0")
	}
	if calls[0].partialText != "hello world" {
		t.Errorf("partial text = %q, want %q", calls[0].partialText, "hello world")
	}
	if !calls[0].recoverable {
		t.Error("periodic flush should mark recoverable=true")
	}
	if calls[0].kind != "transient" {
		t.Errorf("kind = %q, want %q", calls[0].kind, "transient")
	}
}

// TestRunPeriodicFlush_SkipsOnNoNewContent verifies that the flusher
// does NOT call PersistPartial when no new text has been accumulated
// since the last flush (watermark logic).
func TestRunPeriodicFlush_SkipsOnNoNewContent(t *testing.T) {
	t.Parallel()

	broker := &recordingBroker{}
	bridge := NewStreamBridge(broker, "sub-3", "sess-3")
	// No text emitted; bridge stays empty.

	var callCount atomic.Int32
	persister := PartialPersisterFunc(func(_ context.Context, _ string, _ string, _ string, _ bool) (string, error) {
		callCount.Add(1)
		return "id", nil
	})

	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		runPeriodicFlush(ctx, "sess-3", bridge, persister, 30*time.Millisecond)
	}()

	// Let the ticker fire 3 times without any content.
	time.Sleep(150 * time.Millisecond)
	cancel()
	wg.Wait()

	if n := callCount.Load(); n != 0 {
		t.Errorf("expected 0 PersistPartial calls on empty bridge, got %d", n)
	}
}

// TestRunPeriodicFlush_ExitsOnContextCancel verifies the goroutine exits
// cleanly when the run context is cancelled.
func TestRunPeriodicFlush_ExitsOnContextCancel(t *testing.T) {
	t.Parallel()

	broker := &recordingBroker{}
	bridge := NewStreamBridge(broker, "sub-4", "sess-4")
	persister := &fakePartialPersister{}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		runPeriodicFlush(ctx, "sess-4", bridge, persister, 1*time.Second)
	}()

	cancel() // should cause immediate exit
	select {
	case <-done:
		// Good — goroutine exited.
	case <-time.After(500 * time.Millisecond):
		t.Error("runPeriodicFlush did not exit within 500ms of context cancel")
	}
}
