package agentgraph

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// wp07Node builds a minimal tool_dispatch Node for WP07 tests.
func wp07Node(attrs ToolDispatchAttrs) *Node {
	return &Node{
		ID:    "td-wp07",
		Kind:  NodeKindToolDispatch,
		Attrs: attrs,
	}
}

// hangingToolRegistry is a fake ToolRegistry whose Call blocks until its
// context is cancelled, simulating a tool that hangs indefinitely.
type hangingToolRegistry struct{}

func (hangingToolRegistry) Has(_ string) bool                                  { return true }
func (hangingToolRegistry) Call(ctx context.Context, _ ToolCall) (ToolResult, error) {
	<-ctx.Done()
	return ToolResult{}, ctx.Err()
}

// TestDispatch_ToolCallTimeout_HangingToolTimesOut verifies that a tool
// call that hangs beyond env.ToolCallTimeout receives a timeout is_error
// result rather than blocking the loop indefinitely (FR-007 WP07).
func TestDispatch_ToolCallTimeout_HangingToolTimesOut(t *testing.T) {
	node := wp07Node(ToolDispatchAttrs{ParallelDispatch: false, MaxConcurrent: 1})
	env := &Env{
		RunID:           "run-timeout",
		Tools:           hangingToolRegistry{},
		ToolCallTimeout: 100 * time.Millisecond,
	}
	calls := []ToolCallRequest{
		{ID: "call-1", Name: "slow_tool", Arguments: `{}`},
	}
	inputs := PortValues{"tool_calls": calls}

	start := time.Now()
	res, execErr := toolDispatchExecutor{}.Execute(context.Background(), env, node, inputs)
	elapsed := time.Since(start)

	// The executor must complete (not hang).
	if execErr != nil {
		t.Fatalf("Execute returned unexpected error: %v", execErr)
	}

	// Elapsed must be within a generous window around the timeout.
	if elapsed > 2*time.Second {
		t.Errorf("Execute took %v — expected to complete near the 100ms timeout", elapsed)
	}

	// The result must be an is_error with a timeout message.
	results, ok := res.Outputs["tool_results"].([]ToolResult)
	if !ok || len(results) == 0 {
		t.Fatalf("expected tool_results slice, got %T %v", res.Outputs["tool_results"], res.Outputs)
	}
	if !results[0].IsError {
		t.Errorf("expected is_error=true on timeout result, got false")
	}
	if !strings.Contains(results[0].Content, "timed out") {
		t.Errorf("expected 'timed out' in result content, got: %q", results[0].Content)
	}
}

// callWindow records the [start, end] time of a single tool dispatch so
// the test can assert that mutating calls did not overlap.
type callWindow struct {
	name  string
	start time.Time
	end   time.Time
}

// serialCallTracker is a race-safe collector of call windows.
type serialCallTracker struct {
	mu    sync.Mutex
	calls []callWindow
}

// snapshotWindows returns a copy of the recorded windows.
func (t *serialCallTracker) snapshotWindows() []callWindow {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]callWindow, len(t.calls))
	copy(out, t.calls)
	return out
}

// trackingToolRegistry records [start,end] windows for each call.
type trackingToolRegistry struct {
	tracker *serialCallTracker
	delay   time.Duration
}

func (r *trackingToolRegistry) Has(_ string) bool { return true }
func (r *trackingToolRegistry) Call(ctx context.Context, tc ToolCall) (ToolResult, error) {
	start := time.Now()
	select {
	case <-time.After(r.delay):
	case <-ctx.Done():
		return ToolResult{}, ctx.Err()
	}
	end := time.Now()
	r.tracker.mu.Lock()
	r.tracker.calls = append(r.tracker.calls, callWindow{name: tc.Name, start: start, end: end})
	r.tracker.mu.Unlock()
	return ToolResult{Content: "ok"}, nil
}

// TestDispatch_MutatingTools_DoNotInterleave verifies that two concurrently
// dispatched mutating tool calls do not overlap in execution time (FR-007
// mutation-safety — WP07).
func TestDispatch_MutatingTools_DoNotInterleave(t *testing.T) {
	tracker := &serialCallTracker{}
	node := wp07Node(ToolDispatchAttrs{ParallelDispatch: true, MaxConcurrent: 4})
	env := &Env{
		RunID: "run-serialize",
		Tools: &trackingToolRegistry{
			tracker: tracker,
			delay:   30 * time.Millisecond,
		},
		MutatingTools: map[string]bool{
			"write_file": true,
		},
	}
	calls := []ToolCallRequest{
		{ID: "call-1", Name: "write_file", Arguments: `{}`},
		{ID: "call-2", Name: "write_file", Arguments: `{}`},
	}
	inputs := PortValues{"tool_calls": calls}

	_, execErr := toolDispatchExecutor{}.Execute(context.Background(), env, node, inputs)
	if execErr != nil {
		t.Fatalf("Execute returned error: %v", execErr)
	}

	windows := tracker.snapshotWindows()
	if len(windows) != 2 {
		t.Fatalf("expected 2 call windows, got %d", len(windows))
	}
	// Sort so a always runs first.
	a, b := windows[0], windows[1]
	if a.start.After(b.start) {
		a, b = b, a
	}
	// Allow 2ms tolerance for scheduling jitter.
	tolerance := 2 * time.Millisecond
	if a.end.Add(-tolerance).After(b.start) {
		t.Errorf("mutating tools overlapped: first ended %v, second started %v (overlap ~%v)",
			a.end, b.start, a.end.Sub(b.start))
	}
}

// peakTrackingRegistry tracks the peak concurrency of simultaneous Call
// invocations so we can assert read-only tools parallelise.
type peakTrackingRegistry struct {
	active *int64
	peak   *int64
}

func (r *peakTrackingRegistry) Has(_ string) bool { return true }
func (r *peakTrackingRegistry) Call(_ context.Context, _ ToolCall) (ToolResult, error) {
	cur := atomic.AddInt64(r.active, 1)
	defer atomic.AddInt64(r.active, -1)
	// Update peak via CAS loop.
	for {
		p := atomic.LoadInt64(r.peak)
		if cur <= p {
			break
		}
		if atomic.CompareAndSwapInt64(r.peak, p, cur) {
			break
		}
	}
	// Yield so other goroutines can run concurrently.
	time.Sleep(10 * time.Millisecond)
	return ToolResult{Content: "ok"}, nil
}

// TestDispatch_ReadOnlyTools_Parallelize verifies that tools NOT in
// MutatingTools run concurrently (not serialized) even when MutatingTools
// is configured for other tool names (FR-007 WP07 — only mutating tools lock).
func TestDispatch_ReadOnlyTools_Parallelize(t *testing.T) {
	var active, peak int64
	node := wp07Node(ToolDispatchAttrs{ParallelDispatch: true, MaxConcurrent: 4})
	env := &Env{
		RunID: "run-parallel",
		Tools: &peakTrackingRegistry{active: &active, peak: &peak},
		// write_file is mutating; read_file is not.
		MutatingTools: map[string]bool{
			"write_file": true,
		},
	}
	calls := []ToolCallRequest{
		{ID: "c1", Name: "read_file", Arguments: `{}`},
		{ID: "c2", Name: "read_file", Arguments: `{}`},
		{ID: "c3", Name: "read_file", Arguments: `{}`},
	}
	inputs := PortValues{"tool_calls": calls}

	_, execErr := toolDispatchExecutor{}.Execute(context.Background(), env, node, inputs)
	if execErr != nil {
		t.Fatalf("Execute returned error: %v", execErr)
	}

	// With 3 read-only calls and MaxConcurrent=4, peak concurrency must be ≥2
	// to prove they ran in parallel rather than serially.
	if peak < 2 {
		t.Errorf("expected read-only tools to run concurrently (peak≥2), got peak=%d", peak)
	}
}
