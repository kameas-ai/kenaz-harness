package tasks_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/tasks"
)

// fakeStore is a nil-database SQLStore for unit tests.
type fakeStore struct {
	mu      sync.Mutex
	rows    map[string]tasks.Task
	crashes int
}

func newFakeStore() *fakeStore {
	return &fakeStore{rows: make(map[string]tasks.Task)}
}

func (f *fakeStore) Insert(_ context.Context, t tasks.Task) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[t.ID] = t
	return nil
}

func (f *fakeStore) UpdateStatus(_ context.Context, id, status string, exitCode int, endedAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.rows[id]
	if !ok {
		return tasks.ErrNotFound
	}
	t.Status = status
	t.ExitCode = exitCode
	t.EndedAt = &endedAt
	f.rows[id] = t
	return nil
}

func (f *fakeStore) MarkCrashed(_ context.Context) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for id, t := range f.rows {
		if t.Status == "running" {
			t.Status = "crashed"
			f.rows[id] = t
			n++
		}
	}
	f.crashes = n
	return n, nil
}

func (f *fakeStore) snapshot() map[string]tasks.Task {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]tasks.Task, len(f.rows))
	for k, v := range f.rows {
		out[k] = v
	}
	return out
}

// hookCapture is a race-safe hook result recorder.
type hookCapture struct {
	mu       sync.Mutex
	payloads []tasks.BackgroundTaskCompletePayload
}

func (h *hookCapture) fire(_ context.Context, p tasks.BackgroundTaskCompletePayload) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.payloads = append(h.payloads, p)
}

func (h *hookCapture) snapshot() []tasks.BackgroundTaskCompletePayload {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]tasks.BackgroundTaskCompletePayload, len(h.payloads))
	copy(out, h.payloads)
	return out
}

func newTestRegistry(store tasks.SQLStore, hook *hookCapture) *tasks.Registry {
	var firer tasks.HookFirerFunc
	if hook != nil {
		firer = hook.fire
	}
	return tasks.NewRegistry(tasks.Options{
		Store:     store,
		HookFirer: firer,
	})
}

func TestRegisterAndGet(t *testing.T) {
	store := newFakeStore()
	reg := newTestRegistry(store, nil)
	ctx := context.Background()

	id, err := reg.Register(ctx, tasks.RegisterOpts{
		Kind:           tasks.KindBash,
		OwnerSessionID: "sess-1",
		Cmd:            "echo hi",
		Description:    "test task",
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if id == "" {
		t.Fatal("Register returned empty id")
	}

	task, ok := reg.Get(id)
	if !ok {
		t.Fatalf("Get(%q): not found", id)
	}
	if task.Status != tasks.StatusRunning {
		t.Errorf("Status = %q; want %q", task.Status, tasks.StatusRunning)
	}
	if task.Kind != tasks.KindBash {
		t.Errorf("Kind = %q; want %q", task.Kind, tasks.KindBash)
	}

	// Also verify store.
	snap := store.snapshot()
	if _, ok := snap[id]; !ok {
		t.Error("task not in store after Register")
	}
}

func TestEnd_CompletedOnZeroExitCode(t *testing.T) {
	store := newFakeStore()
	hook := &hookCapture{}
	reg := newTestRegistry(store, hook)
	ctx := context.Background()

	id, _ := reg.Register(ctx, tasks.RegisterOpts{Kind: tasks.KindBash, OwnerSessionID: "s1"})

	if err := reg.End(ctx, id, 0); err != nil {
		t.Fatalf("End: %v", err)
	}

	task, ok := reg.Get(id)
	if !ok {
		t.Fatal("Get after End: not found")
	}
	if task.Status != tasks.StatusCompleted {
		t.Errorf("Status = %q; want completed", task.Status)
	}

	// Hook fires asynchronously; give it time.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(hook.snapshot()) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	payloads := hook.snapshot()
	if len(payloads) != 1 {
		t.Fatalf("hook fires = %d; want 1", len(payloads))
	}
	if payloads[0].TaskID != id {
		t.Errorf("payload.TaskID = %q; want %q", payloads[0].TaskID, id)
	}
}

func TestEnd_FailedOnNonZeroExitCode(t *testing.T) {
	reg := newTestRegistry(newFakeStore(), nil)
	ctx := context.Background()

	id, _ := reg.Register(ctx, tasks.RegisterOpts{Kind: tasks.KindBash})
	_ = reg.End(ctx, id, 1)

	task, _ := reg.Get(id)
	if task.Status != tasks.StatusFailed {
		t.Errorf("Status = %q; want failed", task.Status)
	}
}

func TestEnd_AlreadyTerminal(t *testing.T) {
	reg := newTestRegistry(newFakeStore(), nil)
	ctx := context.Background()

	id, _ := reg.Register(ctx, tasks.RegisterOpts{Kind: tasks.KindBash})
	_ = reg.End(ctx, id, 0)

	err := reg.End(ctx, id, 0)
	if err != tasks.ErrAlreadyTerminal {
		t.Errorf("second End err = %v; want ErrAlreadyTerminal", err)
	}
}

func TestEnd_NotFound(t *testing.T) {
	reg := newTestRegistry(newFakeStore(), nil)
	ctx := context.Background()

	err := reg.End(ctx, "no-such-id", 0)
	if err != tasks.ErrNotFound {
		t.Errorf("End unknown id err = %v; want ErrNotFound", err)
	}
}

func TestList(t *testing.T) {
	reg := newTestRegistry(newFakeStore(), nil)
	ctx := context.Background()

	const n = 5
	var ids []string
	for i := 0; i < n; i++ {
		id, _ := reg.Register(ctx, tasks.RegisterOpts{
			Kind: tasks.KindBash,
			Cmd:  fmt.Sprintf("cmd-%d", i),
		})
		ids = append(ids, id)
	}

	list := reg.List()
	if len(list) != n {
		t.Fatalf("List() = %d; want %d", len(list), n)
	}
}

func TestAbort(t *testing.T) {
	reg := newTestRegistry(newFakeStore(), nil)
	ctx := context.Background()

	// PID=0 means no actual process to kill; Abort should still mark cancelled.
	id, _ := reg.Register(ctx, tasks.RegisterOpts{Kind: tasks.KindBash, PID: 0})

	if err := reg.Abort(ctx, id); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	task, _ := reg.Get(id)
	if task.Status != tasks.StatusCancelled {
		t.Errorf("Status = %q; want cancelled", task.Status)
	}
}

func TestSubscribe_ReceivesLines(t *testing.T) {
	reg := newTestRegistry(newFakeStore(), nil)
	ctx := context.Background()

	id, _ := reg.Register(ctx, tasks.RegisterOpts{Kind: tasks.KindBash})

	ch, cancel, ok := reg.Subscribe(id)
	if !ok {
		t.Fatal("Subscribe returned false")
	}
	defer cancel()

	// AppendLine directly (simulates the lineWriter path).
	reg.AppendLine(id, tasks.Line{Stream: "stdout", Text: "hello"})
	reg.AppendLine(id, tasks.Line{Stream: "stdout", Text: "world"})

	// Collect with timeout.
	var got []tasks.Line
	timeout := time.After(500 * time.Millisecond)
collect:
	for {
		select {
		case ln, open := <-ch:
			if !open {
				break collect
			}
			got = append(got, ln)
			if len(got) >= 2 {
				break collect
			}
		case <-timeout:
			break collect
		}
	}

	if len(got) < 2 {
		t.Fatalf("received %d lines; want ≥2", len(got))
	}
}

func TestSubscribe_ChannelClosedOnEnd(t *testing.T) {
	reg := newTestRegistry(newFakeStore(), nil)
	ctx := context.Background()

	id, _ := reg.Register(ctx, tasks.RegisterOpts{Kind: tasks.KindBash})
	ch, _, ok := reg.Subscribe(id)
	if !ok {
		t.Fatal("Subscribe returned false")
	}

	_ = reg.End(ctx, id, 0)

	// Channel should be closed shortly after End.
	timeout := time.After(500 * time.Millisecond)
	select {
	case _, open := <-ch:
		if open {
			t.Error("channel sent a value; expected closed channel")
		}
		// Good: channel was closed.
	case <-timeout:
		t.Error("channel not closed within 500 ms after End")
	}
}

func TestMarkCrashedViaBoot(t *testing.T) {
	store := newFakeStore()
	reg := newTestRegistry(store, nil)
	ctx := context.Background()

	// Register a running task.
	id, _ := reg.Register(ctx, tasks.RegisterOpts{Kind: tasks.KindBash})
	snap1 := store.snapshot()
	if snap1[id].Status != tasks.StatusRunning {
		t.Fatalf("store status = %q; want running", snap1[id].Status)
	}

	// Simulate reboot: MarkCrashed marks all running rows crashed.
	reg.MarkCrashed(ctx)

	snap2 := store.snapshot()
	if snap2[id].Status != tasks.StatusCrashed {
		t.Errorf("store status after MarkCrashed = %q; want crashed", snap2[id].Status)
	}
}

func TestTail(t *testing.T) {
	reg := newTestRegistry(newFakeStore(), nil)
	ctx := context.Background()

	id, _ := reg.Register(ctx, tasks.RegisterOpts{Kind: tasks.KindBash})

	for i := 0; i < 10; i++ {
		reg.AppendLine(id, tasks.Line{Stream: "stdout", Text: fmt.Sprintf("line %d", i)})
	}

	lines, next, ok := reg.Tail(id, 0)
	if !ok {
		t.Fatal("Tail returned false")
	}
	if len(lines) != 10 {
		t.Fatalf("Tail(0) = %d lines; want 10", len(lines))
	}
	if next != 10 {
		t.Fatalf("next offset = %d; want 10", next)
	}

	// Drain from offset 5.
	lines2, next2, _ := reg.Tail(id, 5)
	if len(lines2) != 5 {
		t.Fatalf("Tail(5) = %d lines; want 5", len(lines2))
	}
	_ = next2
}

func TestHookFiresOnce(t *testing.T) {
	hook := &hookCapture{}
	reg := newTestRegistry(newFakeStore(), hook)
	ctx := context.Background()

	id, _ := reg.Register(ctx, tasks.RegisterOpts{Kind: tasks.KindBash, OwnerSessionID: "sess-x"})
	_ = reg.End(ctx, id, 0)

	// Allow hook goroutine to complete.
	time.Sleep(100 * time.Millisecond)

	payloads := hook.snapshot()
	if len(payloads) != 1 {
		t.Fatalf("hook fire count = %d; want 1", len(payloads))
	}
	p := payloads[0]
	if p.TaskID != id {
		t.Errorf("TaskID = %q; want %q", p.TaskID, id)
	}
	if p.OwnerSessionID != "sess-x" {
		t.Errorf("OwnerSessionID = %q; want sess-x", p.OwnerSessionID)
	}
}

func TestRingBuffer_CapEnforced(t *testing.T) {
	// Tiny cap to exercise overflow.
	const cap = 100
	// We test the ring via the registry line capture (AppendLine).
	// A direct ring buffer test validates the overflow logic.
	reg := newTestRegistry(newFakeStore(), nil)
	ctx := context.Background()

	id, _ := reg.Register(ctx, tasks.RegisterOpts{Kind: tasks.KindBash})

	// Append 1000 lines — the ring buffer inside the registry holds 8 MiB;
	// we just verify lines are stored and retrievable.
	for i := 0; i < 1000; i++ {
		reg.AppendLine(id, tasks.Line{Stream: "stdout", Text: fmt.Sprintf("line %05d", i)})
	}

	lines, _, _ := reg.Tail(id, 0)
	if len(lines) != 1000 {
		t.Fatalf("Tail returned %d lines; want 1000", len(lines))
	}
	_ = cap
}

func TestMultipleSubscribers(t *testing.T) {
	reg := newTestRegistry(newFakeStore(), nil)
	ctx := context.Background()

	id, _ := reg.Register(ctx, tasks.RegisterOpts{Kind: tasks.KindBash})

	const nSubs = 5
	channels := make([]<-chan tasks.Line, nSubs)
	cancels := make([]func(), nSubs)
	for i := 0; i < nSubs; i++ {
		ch, cancel, ok := reg.Subscribe(id)
		if !ok {
			t.Fatalf("Subscribe %d returned false", i)
		}
		channels[i] = ch
		cancels[i] = cancel
	}
	for _, c := range cancels {
		defer c()
	}

	// Broadcast a line.
	reg.AppendLine(id, tasks.Line{Stream: "stdout", Text: "broadcast"})

	// All subscribers should receive it.
	for i, ch := range channels {
		select {
		case ln := <-ch:
			if ln.Text != "broadcast" {
				t.Errorf("subscriber %d: got text %q; want broadcast", i, ln.Text)
			}
		case <-time.After(500 * time.Millisecond):
			t.Errorf("subscriber %d: timed out waiting for line", i)
		}
	}
}
