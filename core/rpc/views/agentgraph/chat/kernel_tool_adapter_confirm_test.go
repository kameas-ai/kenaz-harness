package chat

// confirm-each enforcement at the adapter seam
// (confirm-each-enforcement-01PMAG05 WP01).
//
// These are the tests the mission's acceptance criteria name: a
// confirm_each verdict blocks until answered, approve dispatches, deny
// returns a tool error the model can read, a turn's parallel calls share
// one batch id, explicit Cedar deny never prompts, and cancelling the
// run unblocks the pause cleanly.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

// countingToolPool records dispatches. The adapter calls it from the
// test's goroutine but the test body reads the count from another, so
// every access is mutex-guarded and reads go through a snapshot helper
// (CLAUDE.md — race-safe test fakes).
type countingToolPool struct {
	server string
	tool   string

	mu    sync.Mutex
	calls []string
}

func (p *countingToolPool) Tools(_ context.Context) ([]ToolEntry, error) {
	return []ToolEntry{{Server: p.server, Name: p.tool}}, nil
}

func (p *countingToolPool) Call(_ context.Context, server, tool string, _ []byte) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, server+"__"+tool)
	return []byte(`"ok"`), nil
}

func (p *countingToolPool) dispatched() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.calls))
	copy(out, p.calls)
	return out
}

// syncPermResolver is the race-safe sibling of recordingPermResolver:
// the adapter may Resolve from several goroutines in the batch tests.
type syncPermResolver struct {
	verdict PermVerdict

	mu    sync.Mutex
	calls int
}

func (r *syncPermResolver) Resolve(_ context.Context, _, server, tool string) (PermVerdict, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	v := r.verdict
	v.Server, v.Tool = server, tool
	return v, nil
}

func (r *syncPermResolver) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// confirmSpy is the publisher the ConfirmBus fans TopicToolConfirmPending
// onto. Same mutex+snapshot discipline.
type confirmSpy struct {
	mu   sync.Mutex
	seen []toolloop.ConfirmRequest
}

func (s *confirmSpy) publish(req toolloop.ConfirmRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, req)
}

func (s *confirmSpy) snapshot() []toolloop.ConfirmRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]toolloop.ConfirmRequest, len(s.seen))
	copy(out, s.seen)
	return out
}

// callResult carries an adapter Call outcome back to the test body.
type callResult struct {
	res coreag.ToolResult
	err error
}

// awaitParked spins until the bus has at least n parked calls.
func awaitParked(t *testing.T, bus *toolloop.ConfirmBus, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if bus.PendingCount() >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d parked confirmation(s); have %d", n, bus.PendingCount())
}

// confirmFixture wires an adapter with a live ConfirmBus and a scripted
// confirm_each verdict.
type confirmFixture struct {
	pool    *countingToolPool
	perms   *syncPermResolver
	spy     *confirmSpy
	bus     *toolloop.ConfirmBus
	adapter *kernelToolAdapter
}

func newConfirmFixture(t *testing.T, policy string) *confirmFixture {
	t.Helper()
	f := &confirmFixture{
		pool:  &countingToolPool{server: "filesystem", tool: "write_file"},
		perms: &syncPermResolver{verdict: PermVerdict{Policy: policy, Reason: "confirm each use"}},
		spy:   &confirmSpy{},
	}
	f.bus = toolloop.NewConfirmBus(f.spy.publish)
	f.adapter = newKernelToolAdapter(f.pool, f.perms, "sess-confirm").withConfirm(f.bus)
	return f
}

func (f *confirmFixture) call(ctx context.Context, args map[string]any) <-chan callResult {
	out := make(chan callResult, 1)
	go func() {
		res, err := f.adapter.Call(ctx, coreag.ToolCall{Name: "filesystem__write_file", Args: args})
		out <- callResult{res: res, err: err}
	}()
	return out
}

// FR-001: a confirm_each verdict blocks dispatch until the user answers,
// and approving dispatches the call.
func TestKernelToolAdapter_ConfirmEach_BlocksThenApproveDispatches(t *testing.T) {
	t.Parallel()

	f := newConfirmFixture(t, "confirm_each")
	done := f.call(context.Background(), map[string]any{"path": "/etc/hosts"})
	awaitParked(t, f.bus, 1)

	select {
	case r := <-done:
		t.Fatalf("Call returned while awaiting confirmation: %+v", r)
	case <-time.After(50 * time.Millisecond):
	}
	if got := f.pool.dispatched(); len(got) != 0 {
		t.Fatalf("tool dispatched before approval: %v", got)
	}

	events := f.spy.snapshot()
	if len(events) != 1 {
		t.Fatalf("published %d events, want 1", len(events))
	}
	ev := events[0]
	if ev.SessionID != "sess-confirm" || ev.Server != "filesystem" || ev.Tool != "write_file" {
		t.Fatalf("event identity wrong: %+v", ev)
	}
	if ev.CallID == "" || ev.BatchID == "" {
		t.Fatalf("event must carry call and batch ids: %+v", ev)
	}
	if ev.Reason != "confirm each use" {
		t.Fatalf("event reason = %q, want the resolver reason", ev.Reason)
	}

	if err := f.bus.Resolve(ev.SessionID, ev.CallID, toolloop.ConfirmDecision{Approved: true}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("Call err = %v", r.err)
		}
		if r.res.IsError {
			t.Fatalf("approved call returned an error result: %q", r.res.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Call did not return after approval")
	}
	if got := f.pool.dispatched(); len(got) != 1 || got[0] != "filesystem__write_file" {
		t.Fatalf("dispatched = %v, want one filesystem__write_file", got)
	}
}

// Deny returns a tool error the model can read — same shape as a policy
// deny — and never dispatches.
func TestKernelToolAdapter_ConfirmEach_DenyReturnsToolError(t *testing.T) {
	t.Parallel()

	f := newConfirmFixture(t, "confirm_each")
	done := f.call(context.Background(), map[string]any{"path": "/etc/hosts"})
	awaitParked(t, f.bus, 1)

	ev := f.spy.snapshot()[0]
	if err := f.bus.Resolve(ev.SessionID, ev.CallID,
		toolloop.ConfirmDecision{Approved: false, Reason: "user denied"}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	r := <-done
	if r.err != nil {
		t.Fatalf("Call err = %v, want nil (denial is a tool error, not a Go error)", r.err)
	}
	if !r.res.IsError {
		t.Fatal("denied call returned IsError=false")
	}
	if !strings.Contains(r.res.Content, `tool "filesystem__write_file" denied`) ||
		!strings.Contains(r.res.Content, "user denied") {
		t.Fatalf("denial content = %q, want the standard tool-error shape with the reason", r.res.Content)
	}
	if got := f.pool.dispatched(); len(got) != 0 {
		t.Fatalf("denied call still dispatched: %v", got)
	}
}

// Explicit dismissal (window close) is a denial, never an allow (FR-003).
func TestKernelToolAdapter_ConfirmEach_DismissalDenies(t *testing.T) {
	t.Parallel()

	f := newConfirmFixture(t, "confirm_each")
	done := f.call(context.Background(), nil)
	awaitParked(t, f.bus, 1)

	ev := f.spy.snapshot()[0]
	if n := f.bus.CancelBatch(ev.BatchID, "dialog dismissed"); n != 1 {
		t.Fatalf("CancelBatch cancelled %d, want 1", n)
	}

	r := <-done
	if !r.res.IsError {
		t.Fatal("dismissal produced a successful dispatch")
	}
	if !strings.Contains(r.res.Content, "dialog dismissed") {
		t.Fatalf("content = %q, want the dismissal reason", r.res.Content)
	}
	if got := f.pool.dispatched(); len(got) != 0 {
		t.Fatalf("dismissed call dispatched: %v", got)
	}
}

// FR-005: explicit Cedar deny short-circuits BEFORE any prompt — the
// user is never shown an approvable row for a call policy already
// forbids.
func TestKernelToolAdapter_ConfirmEach_CedarDenyNeverPrompts(t *testing.T) {
	t.Parallel()

	f := newConfirmFixture(t, "deny")
	f.perms.verdict.Reason = "cedar forbid rule matched"

	res, err := f.adapter.Call(context.Background(),
		coreag.ToolCall{Name: "filesystem__write_file", Args: map[string]any{"path": "/x"}})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !res.IsError {
		t.Fatal("deny verdict was not honoured")
	}
	if !strings.Contains(res.Content, "cedar forbid rule matched") {
		t.Fatalf("content = %q, want the policy reason", res.Content)
	}
	if got := f.spy.snapshot(); len(got) != 0 {
		t.Fatalf("deny published a confirmation prompt: %+v", got)
	}
	if n := f.bus.PendingCount(); n != 0 {
		t.Fatalf("deny parked %d confirmations, want 0", n)
	}
	if got := f.pool.dispatched(); len(got) != 0 {
		t.Fatalf("denied call dispatched: %v", got)
	}
}

// An auto_allow verdict dispatches without ever touching the bus.
func TestKernelToolAdapter_AutoAllow_NeverPrompts(t *testing.T) {
	t.Parallel()

	f := newConfirmFixture(t, "auto_allow")
	res, err := f.adapter.Call(context.Background(),
		coreag.ToolCall{Name: "filesystem__write_file"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if res.IsError {
		t.Fatalf("auto_allow returned an error: %q", res.Content)
	}
	if got := f.spy.snapshot(); len(got) != 0 {
		t.Fatalf("auto_allow published a prompt: %+v", got)
	}
	if got := f.pool.dispatched(); len(got) != 1 {
		t.Fatalf("dispatched = %v, want one call", got)
	}
}

// A turn's parallel calls register N pendings sharing one batch id, and
// rows resolve independently: approving one dispatches it while the rest
// stay parked (plan.md — no all-or-nothing coupling).
func TestKernelToolAdapter_ConfirmEach_BatchSharesBatchID(t *testing.T) {
	t.Parallel()

	f := newConfirmFixture(t, "confirm_each")
	ctx := toolloop.WithConfirmBatch(context.Background(), "turn-42")

	const n = 3
	results := make([]<-chan callResult, 0, n)
	for i := 0; i < n; i++ {
		results = append(results, f.call(ctx, map[string]any{"path": "/tmp/x"}))
	}
	awaitParked(t, f.bus, n)

	events := f.spy.snapshot()
	if len(events) != n {
		t.Fatalf("published %d events, want %d", len(events), n)
	}
	ids := map[string]bool{}
	for _, ev := range events {
		if ev.BatchID != "turn-42" {
			t.Fatalf("event %q batch = %q, want turn-42 (one modal per turn)", ev.CallID, ev.BatchID)
		}
		if ids[ev.CallID] {
			t.Fatalf("duplicate call id %q", ev.CallID)
		}
		ids[ev.CallID] = true
	}
	if got := f.perms.count(); got != n {
		t.Fatalf("resolver consulted %d times, want %d", got, n)
	}

	// Approve one row.
	if err := f.bus.Resolve("sess-confirm", events[0].CallID,
		toolloop.ConfirmDecision{Approved: true}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// Exactly one row unblocks; the others are still parked.
	awaitDispatched(t, f.pool, 1)
	if n := f.bus.PendingCount(); n != 2 {
		t.Fatalf("PendingCount = %d after one approval, want 2", n)
	}

	// Deny the rest via the batch — window close is deny-all.
	if got := f.bus.CancelBatch("turn-42", "dismissed"); got != n-1 {
		t.Fatalf("CancelBatch = %d, want %d", got, n-1)
	}
	denied := 0
	for _, ch := range results {
		if r := <-ch; r.res.IsError {
			denied++
		}
	}
	if denied != n-1 {
		t.Fatalf("%d rows denied, want %d", denied, n-1)
	}
	if got := f.pool.dispatched(); len(got) != 1 {
		t.Fatalf("dispatched = %v, want exactly the approved row", got)
	}
}

func awaitDispatched(t *testing.T, p *countingToolPool, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(p.dispatched()) >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d dispatch(es); have %d", n, len(p.dispatched()))
}

// Ungrouped callers still get a batch — of one. The frontend renders a
// single-row modal rather than special-casing an empty batch id.
func TestKernelToolAdapter_ConfirmEach_UngroupedCallGetsOwnBatch(t *testing.T) {
	t.Parallel()

	f := newConfirmFixture(t, "confirm_each")
	done := f.call(context.Background(), nil)
	awaitParked(t, f.bus, 1)

	ev := f.spy.snapshot()[0]
	if !strings.HasPrefix(ev.BatchID, "batch-") {
		t.Fatalf("batch id = %q, want a generated batch- id", ev.BatchID)
	}
	_ = f.bus.Cancel(ev.SessionID, ev.CallID, "teardown")
	<-done
}

// Cancelling the run's context unblocks the parked call: the turn aborts
// with an error rather than hanging or silently dispatching.
func TestKernelToolAdapter_ConfirmEach_ContextCancellationUnblocks(t *testing.T) {
	t.Parallel()

	f := newConfirmFixture(t, "confirm_each")
	ctx, cancel := context.WithCancel(context.Background())
	done := f.call(ctx, nil)
	awaitParked(t, f.bus, 1)

	cancel()

	select {
	case r := <-done:
		if r.err == nil {
			t.Fatalf("Call returned nil error on cancellation; result = %+v", r.res)
		}
		if !errors.Is(r.err, context.Canceled) {
			t.Fatalf("err = %v, want a wrapped context.Canceled", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Call did not unblock when the run context was cancelled")
	}
	if got := f.pool.dispatched(); len(got) != 0 {
		t.Fatalf("cancelled call dispatched: %v", got)
	}
	if n := f.bus.PendingCount(); n != 0 {
		t.Fatalf("PendingCount = %d after cancellation, want 0", n)
	}
}

// No confirmation channel ⇒ deny, not the silent auto-allow this mission
// exists to remove. WP05 makes the headless policy configurable; the
// default stays deny.
func TestKernelToolAdapter_ConfirmEach_NilBusDenies(t *testing.T) {
	t.Parallel()

	pool := &countingToolPool{server: "filesystem", tool: "write_file"}
	perms := &syncPermResolver{verdict: PermVerdict{Policy: "confirm_each"}}
	adapter := newKernelToolAdapter(pool, perms, "sess-headless") // no withConfirm

	res, err := adapter.Call(context.Background(),
		coreag.ToolCall{Name: "filesystem__write_file"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !res.IsError {
		t.Fatal("confirm_each with no confirmation channel auto-allowed — that is the defect")
	}
	if !strings.Contains(res.Content, "no confirmation channel") {
		t.Fatalf("content = %q, want it to name the missing channel", res.Content)
	}
	if got := pool.dispatched(); len(got) != 0 {
		t.Fatalf("dispatched without a confirmation channel: %v", got)
	}
}

// The published payload carries a structural args summary only — never a
// raw argument value (redaction contract on TopicToolConfirmPending).
func TestKernelToolAdapter_ConfirmEach_PayloadRedactsArgValues(t *testing.T) {
	t.Parallel()

	f := newConfirmFixture(t, "confirm_each")
	const secret = "sk-live-DEADBEEF"
	done := f.call(context.Background(), map[string]any{
		"api_key": secret,
		"path":    "/home/alec/.ssh/id_rsa",
		"count":   float64(3),
	})
	awaitParked(t, f.bus, 1)

	ev := f.spy.snapshot()[0]
	if strings.Contains(ev.ArgsSummary, secret) || strings.Contains(ev.ArgsSummary, "id_rsa") {
		t.Fatalf("args summary leaked a value: %q", ev.ArgsSummary)
	}
	want := "3 arguments: api_key (string), count (number), path (string)"
	if ev.ArgsSummary != want {
		t.Fatalf("ArgsSummary = %q, want %q", ev.ArgsSummary, want)
	}

	_ = f.bus.Cancel(ev.SessionID, ev.CallID, "teardown")
	<-done
}
