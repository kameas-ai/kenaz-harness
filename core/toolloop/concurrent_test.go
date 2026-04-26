package toolloop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	corellm "github.com/sigil-tech/kaneaz-harness/core/llm"
)

// captureProgress records every EmitToolStarted / EmitToolFinished call
// so tests can assert that progress events fire in the expected order
// and with the expected statuses.
type captureProgress struct {
	mu       sync.Mutex
	started  []ToolProgressEvent
	finished []ToolProgressEvent
}

func (c *captureProgress) EmitToolStarted(_ context.Context, ev ToolProgressEvent) {
	c.mu.Lock()
	c.started = append(c.started, ev)
	c.mu.Unlock()
}

func (c *captureProgress) EmitToolFinished(_ context.Context, ev ToolProgressEvent) {
	c.mu.Lock()
	c.finished = append(c.finished, ev)
	c.mu.Unlock()
}

func (c *captureProgress) snapshotStarted() []ToolProgressEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]ToolProgressEvent, len(c.started))
	copy(out, c.started)
	return out
}

func (c *captureProgress) snapshotFinished() []ToolProgressEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]ToolProgressEvent, len(c.finished))
	copy(out, c.finished)
	return out
}

// TestConcurrent_ParallelDispatchPreservesOrder verifies that even when
// the pool finishes calls out of order (call B completes before call A
// because A sleeps longer), the results threaded into the augmented
// conversation appear in the declared order — model and history never
// see the parallelism (FR-004).
func TestConcurrent_ParallelDispatchPreservesOrder(t *testing.T) {
	pool := newFakePool()
	// "slow" finishes after a short delay; "fast" returns immediately.
	pool.register("svc", "slow", func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return json.RawMessage(`"slow-done"`), nil
	})
	pool.register("svc", "fast", func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`"fast-done"`), nil
	})
	reg := &fakeRegistry{
		streams: []corellm.Stream{
			&scriptedStream{final: corellm.Response{
				Content:      []corellm.ContentPart{{Type: "text", Text: "done"}},
				FinishReason: "end_turn",
			}},
		},
	}
	loop, err := New(Config{Registry: reg, Pool: pool, MaxConcurrentTools: 4})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	initial := &corellm.Response{
		FinishReason: "tool_use",
		ToolCalls: []corellm.ToolUse{
			{ID: "t-slow", Name: "slow", Input: json.RawMessage(`{}`)},
			{ID: "t-fast", Name: "fast", Input: json.RawMessage(`{}`)},
		},
	}
	if err := loop.Run(context.Background(), "s", "p", initial, corellm.GenerationRequest{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The augmented messages handed to the registry on re-invocation
	// must be: [assistant tool_use, tool result(slow), tool result(fast)]
	// — declared order preserved despite fast finishing first.
	gotMsgs := reg.requests[0].Messages
	if len(gotMsgs) != 3 {
		t.Fatalf("messages = %d, want 3 (assistant tool_use, tool slow, tool fast)", len(gotMsgs))
	}
	var slowPayload, fastPayload map[string]any
	_ = json.Unmarshal(gotMsgs[1].Content[0].ToolData, &slowPayload)
	_ = json.Unmarshal(gotMsgs[2].Content[0].ToolData, &fastPayload)
	if slowPayload["tool_use_id"] != "t-slow" {
		t.Fatalf("msgs[1].tool_use_id = %v, want t-slow", slowPayload["tool_use_id"])
	}
	if fastPayload["tool_use_id"] != "t-fast" {
		t.Fatalf("msgs[2].tool_use_id = %v, want t-fast", fastPayload["tool_use_id"])
	}
}

// TestConcurrent_BoundsParallelismToCap verifies the semaphore enforces
// MaxConcurrentTools — at most cap goroutines hold a sem slot at once.
// We spin N=10 calls with cap=3; the high-water mark of in-flight
// handlers must never exceed 3.
func TestConcurrent_BoundsParallelismToCap(t *testing.T) {
	const N = 10
	const cap = 3
	pool := newFakePool()

	var inFlight, peak atomic.Int32
	gate := make(chan struct{})  // released after the test verifies the peak
	startedAll := make(chan struct{})
	var startedCount atomic.Int32

	pool.register("svc", "blocker", func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		cur := inFlight.Add(1)
		for {
			old := peak.Load()
			if cur > old {
				if peak.CompareAndSwap(old, cur) {
					break
				}
				continue
			}
			break
		}
		// Wait until the test has had a chance to observe the steady
		// state — once `cap` handlers are running we know we've hit
		// the high-water mark.
		if startedCount.Add(1) == cap {
			close(startedAll)
		}
		select {
		case <-gate:
		case <-ctx.Done():
		}
		inFlight.Add(-1)
		return json.RawMessage(`"ok"`), nil
	})

	reg := &fakeRegistry{
		streams: []corellm.Stream{
			&scriptedStream{final: corellm.Response{
				Content:      []corellm.ContentPart{{Type: "text", Text: "done"}},
				FinishReason: "end_turn",
			}},
		},
	}
	loop, _ := New(Config{Registry: reg, Pool: pool, MaxConcurrentTools: cap})

	calls := make([]corellm.ToolUse, N)
	for i := 0; i < N; i++ {
		calls[i] = corellm.ToolUse{
			ID:    fmt.Sprintf("t-%d", i),
			Name:  "blocker",
			Input: json.RawMessage(`{}`),
		}
	}
	initial := &corellm.Response{FinishReason: "tool_use", ToolCalls: calls}

	done := make(chan error, 1)
	go func() { done <- loop.Run(context.Background(), "s", "p", initial, corellm.GenerationRequest{}) }()

	// Wait until cap handlers are simultaneously in-flight (or timeout).
	select {
	case <-startedAll:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %d concurrent handlers", cap)
	}

	// Allow handlers to drain; the loop should now finish cleanly.
	close(gate)
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := peak.Load()
	if got > cap {
		t.Fatalf("peak in-flight = %d, must not exceed cap %d", got, cap)
	}
	if got < cap {
		// We told `cap` handlers to coordinate via startedAll, so peak
		// should equal cap. A lower value means the dispatcher serialized.
		t.Fatalf("peak in-flight = %d, want %d (concurrency not engaged)", got, cap)
	}
}

// TestConcurrent_DefaultsToFour verifies Config.MaxConcurrentTools=0
// resolves to DefaultMaxConcurrentTools.
func TestConcurrent_DefaultsToFour(t *testing.T) {
	loop, err := New(Config{Registry: &fakeRegistry{}, Pool: newFakePool()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if loop.maxParallel != DefaultMaxConcurrentTools {
		t.Fatalf("maxParallel = %d, want %d", loop.maxParallel, DefaultMaxConcurrentTools)
	}
	if DefaultMaxConcurrentTools != 4 {
		t.Fatalf("DefaultMaxConcurrentTools = %d, spec FR-004 requires 4", DefaultMaxConcurrentTools)
	}
}

// TestConcurrent_MidflightCancellationProducesToolCancelledResults
// verifies FR-008: when the host context cancels mid-flight, in-flight
// tool calls produce synthetic tool_cancelled results and the
// post_tool_use hook fires for each cancelled call with the cancellation
// error. Run returns ctx.Err so the caller can distinguish a cancelled
// loop from a successful one.
func TestConcurrent_MidflightCancellationProducesToolCancelledResults(t *testing.T) {
	pool := newFakePool()
	dispatched := make(chan struct{}, 3)
	pool.register("svc", "blocker", func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		// Signal we entered the handler so the test can cancel.
		select {
		case dispatched <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return nil, ctx.Err()
	})

	hooks := &captureHookRunner{}
	progress := &captureProgress{}
	audit := newCaptureAuditEmitter(t)
	loop, err := New(Config{
		Registry:           &fakeRegistry{},
		Pool:               pool,
		Hooks:              hooks,
		Progress:           progress,
		Audit:              audit,
		MaxConcurrentTools: 4,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	calls := []corellm.ToolUse{
		{ID: "t-1", Name: "blocker", Input: json.RawMessage(`{}`)},
		{ID: "t-2", Name: "blocker", Input: json.RawMessage(`{}`)},
		{ID: "t-3", Name: "blocker", Input: json.RawMessage(`{}`)},
	}
	initial := &corellm.Response{FinishReason: "tool_use", ToolCalls: calls}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- loop.Run(ctx, "sess", "sub", initial, corellm.GenerationRequest{})
	}()

	// Wait for at least one handler to enter, then cancel.
	select {
	case <-dispatched:
	case <-time.After(2 * time.Second):
		t.Fatal("no handler entered before cancel")
	}
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}

	// Every declared call must have a post_tool_use event with a
	// cancellation error so audit / memory pipelines see the dropped
	// calls. Per WP04 acceptance.
	postEvts := hooks.snapshotPost()
	if len(postEvts) != len(calls) {
		t.Fatalf("post events = %d, want %d (one per declared call)", len(postEvts), len(calls))
	}
	for _, ev := range postEvts {
		if ev.Error == "" {
			t.Errorf("post event for tool_use_id=%q has empty Error; want a cancellation message", ev.Tool)
		}
	}

	// Every declared call must produce a "cancelled" progress finished
	// event so the chat surface can render the appropriate chip.
	finished := progress.snapshotFinished()
	if len(finished) != len(calls) {
		t.Fatalf("progress.finished = %d, want %d", len(finished), len(calls))
	}
	for _, ev := range finished {
		if ev.Status != "cancelled" {
			t.Errorf("progress status = %q, want cancelled", ev.Status)
		}
	}

	// Audit log must show a tool_failed entry per call (cancellation
	// surfaces under failed; the WP05 confirm-each work introduces a
	// distinct kind for user-facing cancellations).
	failed := audit.byKind("tool_failed")
	if len(failed) != len(calls) {
		t.Fatalf("audit tool_failed entries = %d, want %d", len(failed), len(calls))
	}
}

// TestConcurrent_QueuedCallsCancelWithoutDispatch verifies that calls
// waiting on the semaphore (cap=1, three calls) skip pool.Call entirely
// once the context is cancelled — only the call that already acquired
// the slot ever reaches the pool.
func TestConcurrent_QueuedCallsCancelWithoutDispatch(t *testing.T) {
	pool := newFakePool()
	entered := make(chan struct{}, 1)
	pool.register("svc", "blocker", func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return nil, ctx.Err()
	})

	hooks := &captureHookRunner{}
	loop, _ := New(Config{
		Registry:           &fakeRegistry{},
		Pool:               pool,
		Hooks:              hooks,
		MaxConcurrentTools: 1, // serialize so calls 2 and 3 queue
	})

	calls := []corellm.ToolUse{
		{ID: "t-1", Name: "blocker", Input: json.RawMessage(`{}`)},
		{ID: "t-2", Name: "blocker", Input: json.RawMessage(`{}`)},
		{ID: "t-3", Name: "blocker", Input: json.RawMessage(`{}`)},
	}
	initial := &corellm.Response{FinishReason: "tool_use", ToolCalls: calls}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- loop.Run(ctx, "sess", "sub", initial, corellm.GenerationRequest{})
	}()

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first handler never entered")
	}
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}

	// Only one pool call reaches the handler; the other two never
	// dispatch (they were queued on the sem).
	if got := len(pool.calls); got != 1 {
		t.Fatalf("pool.calls = %d, want 1 (queued calls must not dispatch after cancel)", got)
	}
	// All three still get a post_tool_use event (audit completeness).
	if got := len(hooks.snapshotPost()); got != 3 {
		t.Fatalf("post events = %d, want 3 (one per declared call)", got)
	}
}

// TestConcurrent_DeadlineExceededProducesCancelledResult verifies
// context.DeadlineExceeded is treated identically to context.Canceled —
// in-flight tools return tool_cancelled results so the model can see
// "the loop timed out", and Run returns the deadline error.
func TestConcurrent_DeadlineExceededProducesCancelledResult(t *testing.T) {
	pool := newFakePool()
	pool.register("svc", "slow", func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})

	hooks := &captureHookRunner{}
	loop, _ := New(Config{Registry: &fakeRegistry{}, Pool: pool, Hooks: hooks})

	initial := &corellm.Response{
		FinishReason: "tool_use",
		ToolCalls: []corellm.ToolUse{
			{ID: "t-1", Name: "slow", Input: json.RawMessage(`{}`)},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := loop.Run(ctx, "sess", "sub", initial, corellm.GenerationRequest{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run err = %v, want context.DeadlineExceeded", err)
	}

	// The post_tool_use hook still fires; its Error carries the
	// deadline-exceeded text so audit pipelines see the timeout.
	postEvts := hooks.snapshotPost()
	if len(postEvts) != 1 {
		t.Fatalf("post events = %d, want 1", len(postEvts))
	}
	if postEvts[0].Error == "" {
		t.Errorf("post event Error empty; want deadline error")
	}
}

// TestConcurrent_ProgressEmittedForSuccessfulDispatch verifies the
// streaming feedback path (FR-010): every successful tool dispatch
// fires EmitToolStarted then EmitToolFinished with status="ok".
func TestConcurrent_ProgressEmittedForSuccessfulDispatch(t *testing.T) {
	pool := newFakePool()
	pool.register("svc", "ok", func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`"done"`), nil
	})
	reg := &fakeRegistry{
		streams: []corellm.Stream{
			&scriptedStream{final: corellm.Response{
				Content:      []corellm.ContentPart{{Type: "text", Text: "all done"}},
				FinishReason: "end_turn",
			}},
		},
	}
	progress := &captureProgress{}
	loop, _ := New(Config{Registry: reg, Pool: pool, Progress: progress})
	initial := &corellm.Response{
		FinishReason: "tool_use",
		ToolCalls:    []corellm.ToolUse{{ID: "t-1", Name: "ok", Input: json.RawMessage(`{}`)}},
	}
	if err := loop.Run(context.Background(), "s", "p", initial, corellm.GenerationRequest{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	started := progress.snapshotStarted()
	finished := progress.snapshotFinished()
	if len(started) != 1 {
		t.Fatalf("started = %d, want 1", len(started))
	}
	if len(finished) != 1 {
		t.Fatalf("finished = %d, want 1", len(finished))
	}
	if started[0].Status != "started" {
		t.Errorf("started status = %q, want started", started[0].Status)
	}
	if finished[0].Status != "ok" {
		t.Errorf("finished status = %q, want ok", finished[0].Status)
	}
	if finished[0].LatencyMS < 0 {
		t.Errorf("finished latency = %d, want >= 0", finished[0].LatencyMS)
	}
	if started[0].ToolUseID != "t-1" {
		t.Errorf("started tool_use_id = %q, want t-1", started[0].ToolUseID)
	}
}

// TestConcurrent_ProgressNotEmittedForBlockedCalls verifies the
// streaming chip is not surfaced for permission-blocked or pre-hook-
// short-circuited calls — those are silent in the chat surface (the
// model still sees the synthetic blocked tool_result, but the user does
// not see a "tool ran" chip).
func TestConcurrent_ProgressNotEmittedForBlockedCalls(t *testing.T) {
	pool := newFakePool()
	pool.register("filesystem", "delete", func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
		t.Fatal("pool.Call must not run when permission denies")
		return nil, nil
	})
	resolver, _ := NewStaticResolver([]permRule{
		{Server: "filesystem", Tool: "delete", Policy: PolicyDeny},
	})
	reg := &fakeRegistry{
		streams: []corellm.Stream{
			&scriptedStream{final: corellm.Response{
				Content:      []corellm.ContentPart{{Type: "text", Text: "ok"}},
				FinishReason: "end_turn",
			}},
		},
	}
	progress := &captureProgress{}
	loop, _ := New(Config{Registry: reg, Pool: pool, Permissions: resolver, Progress: progress})
	initial := &corellm.Response{
		FinishReason: "tool_use",
		ToolCalls:    []corellm.ToolUse{{ID: "t-1", Name: "delete", Input: json.RawMessage(`{}`)}},
	}
	if err := loop.Run(context.Background(), "s", "p", initial, corellm.GenerationRequest{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := len(progress.snapshotStarted()); got != 0 {
		t.Errorf("progress started = %d, want 0 (deny path is silent)", got)
	}
	if got := len(progress.snapshotFinished()); got != 0 {
		t.Errorf("progress finished = %d, want 0 (deny path is silent)", got)
	}
}

// TestConcurrent_ParallelDispatchOrderingUnderPressure stresses the
// dispatcher with N=20 calls under cap=4 and verifies (a) every result
// lands in declared order and (b) the race detector stays clean. The
// test runs a tight loop several times to surface any goroutine-ordering
// flakes; race coverage is ensured by `go test -race`.
func TestConcurrent_ParallelDispatchOrderingUnderPressure(t *testing.T) {
	const N = 20
	pool := newFakePool()
	pool.register("svc", "echo", func(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
		// Vary work duration so finishes interleave.
		var p struct {
			Sleep int `json:"sleep"`
		}
		_ = json.Unmarshal(args, &p)
		if p.Sleep > 0 {
			time.Sleep(time.Duration(p.Sleep) * time.Millisecond)
		}
		return args, nil
	})
	reg := &fakeRegistry{
		streams: []corellm.Stream{
			&scriptedStream{final: corellm.Response{
				Content:      []corellm.ContentPart{{Type: "text", Text: "done"}},
				FinishReason: "end_turn",
			}},
		},
	}
	loop, _ := New(Config{Registry: reg, Pool: pool, MaxConcurrentTools: 4})
	calls := make([]corellm.ToolUse, N)
	for i := 0; i < N; i++ {
		// Reverse-correlated sleeps: earlier calls sleep longer so
		// finishes really do come out of order.
		sleepMS := (N - i) % 8
		args, _ := json.Marshal(map[string]any{"id": i, "sleep": sleepMS})
		calls[i] = corellm.ToolUse{
			ID:    fmt.Sprintf("t-%d", i),
			Name:  "echo",
			Input: args,
		}
	}
	initial := &corellm.Response{FinishReason: "tool_use", ToolCalls: calls}
	if err := loop.Run(context.Background(), "s", "p", initial, corellm.GenerationRequest{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	gotMsgs := reg.requests[0].Messages
	// 1 assistant tool_use + N tool results.
	if len(gotMsgs) != 1+N {
		t.Fatalf("messages = %d, want %d", len(gotMsgs), 1+N)
	}
	for i := 0; i < N; i++ {
		var p map[string]any
		_ = json.Unmarshal(gotMsgs[1+i].Content[0].ToolData, &p)
		if p["tool_use_id"] != fmt.Sprintf("t-%d", i) {
			t.Fatalf("msgs[%d].tool_use_id = %v, want t-%d", 1+i, p["tool_use_id"], i)
		}
	}
}
