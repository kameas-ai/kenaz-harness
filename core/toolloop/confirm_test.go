package toolloop

// ConfirmBus tests (confirm-each-enforcement-01PMAG05 WP01).
//
// The bus is the pause primitive behind a `confirm_each` verdict. These
// tests pin the properties the mission's acceptance criteria name:
// parking blocks, resolution unblocks with the user's answer, dismissal
// is a denial, a turn's parallel calls share one batch ID, unrelated
// sessions are never coupled, and context cancellation unblocks cleanly.
//
// There is deliberately no "times out" test: absence of an answer is not
// an outcome (owner decision 1). Two tests guard that together —
// TestConfirmBus_NoDeadline_ParksIndefinitely watches a parked call stay
// parked, and TestConfirm_SourceHasNoDeadlinePrimitives reads confirm.go
// and fails on any time.After / NewTimer / WithTimeout / WithDeadline.
// The second exists because the first cannot fail against a deadline
// longer than its own window.

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordingPublisher captures published ConfirmRequests. Writes arrive
// from the parking goroutine while the test body reads, so every access
// goes through the mutex and reads use snapshot().
type recordingPublisher struct {
	mu   sync.Mutex
	seen []ConfirmRequest
}

func (p *recordingPublisher) publish(req ConfirmRequest) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.seen = append(p.seen, req)
}

func (p *recordingPublisher) snapshot() []ConfirmRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]ConfirmRequest, len(p.seen))
	copy(out, p.seen)
	return out
}

// awaitResult carries a Pending() outcome back to the test body.
type awaitResult struct {
	decision ConfirmDecision
	err      error
}

// park starts a Pending() call in the background and returns the channel
// its outcome will arrive on plus a helper that waits for the bus to
// actually register the entry (so tests never race the goroutine start).
func park(t *testing.T, b *ConfirmBus, ctx context.Context, req ConfirmRequest) <-chan awaitResult {
	t.Helper()
	out := make(chan awaitResult, 1)
	go func() {
		d, err := b.Pending(ctx, req)
		out <- awaitResult{decision: d, err: err}
	}()
	waitForPending(t, b, 1)
	return out
}

// waitForPending blocks until the bus reports at least n parked calls.
func waitForPending(t *testing.T, b *ConfirmBus, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if b.PendingCount() >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d pending confirmation(s); have %d", n, b.PendingCount())
}

func req(session, call, batch, tool string) ConfirmRequest {
	return ConfirmRequest{
		SessionID:   session,
		CallID:      call,
		BatchID:     batch,
		Server:      "filesystem",
		Tool:        tool,
		ArgsSummary: "1 argument: path (string)",
	}
}

// A parked call blocks until somebody resolves it — no clock converts
// waiting into an answer.
func TestConfirmBus_BlocksUntilResolved(t *testing.T) {
	t.Parallel()

	pub := &recordingPublisher{}
	bus := NewConfirmBus(pub.publish)
	done := park(t, bus, context.Background(), req("sess", "call-1", "batch-1", "write_file"))

	select {
	case r := <-done:
		t.Fatalf("Pending returned before any decision: %+v", r)
	case <-time.After(50 * time.Millisecond):
		// Still parked — correct.
	}

	if got := pub.snapshot(); len(got) != 1 || got[0].CallID != "call-1" {
		t.Fatalf("published events = %+v, want exactly one for call-1", got)
	}

	if err := bus.Resolve("sess", "call-1", ConfirmDecision{Approved: true}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("Pending err = %v, want nil", r.err)
		}
		if !r.decision.Approved {
			t.Fatal("decision.Approved = false, want true")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Pending did not return after Resolve")
	}

	if n := bus.PendingCount(); n != 0 {
		t.Fatalf("PendingCount after resolve = %d, want 0", n)
	}
}

// Explicit dismissal resolves to deny (FR-003) — never to allow.
func TestConfirmBus_CancelIsDenial(t *testing.T) {
	t.Parallel()

	bus := NewConfirmBus(nil)
	done := park(t, bus, context.Background(), req("sess", "call-1", "batch-1", "rm"))

	if err := bus.Cancel("sess", "call-1", "dismissed"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	r := <-done
	if r.err != nil {
		t.Fatalf("Pending err = %v, want nil", r.err)
	}
	if r.decision.Approved {
		t.Fatal("dismissal produced Approved=true — dismissal must deny")
	}
	if r.decision.Reason != "dismissed" {
		t.Fatalf("decision.Reason = %q, want %q", r.decision.Reason, "dismissed")
	}
}

// N parallel calls from one turn register N pendings sharing a batch ID,
// and CancelBatch (window close) denies all of them at once.
func TestConfirmBus_BatchGrouping_AndCancelBatch(t *testing.T) {
	t.Parallel()

	pub := &recordingPublisher{}
	bus := NewConfirmBus(pub.publish)
	ctx := context.Background()

	const n = 4
	results := make([]<-chan awaitResult, 0, n)
	for i := 0; i < n; i++ {
		out := make(chan awaitResult, 1)
		r := req("sess", "call-"+string(rune('a'+i)), "batch-shared", "tool")
		go func() {
			d, err := bus.Pending(ctx, r)
			out <- awaitResult{decision: d, err: err}
		}()
		results = append(results, out)
	}
	waitForPending(t, bus, n)

	events := pub.snapshot()
	if len(events) != n {
		t.Fatalf("published events = %d, want %d (one per parked call)", len(events), n)
	}
	seenCalls := map[string]bool{}
	for _, e := range events {
		if e.BatchID != "batch-shared" {
			t.Fatalf("event %q batch = %q, want the shared batch id", e.CallID, e.BatchID)
		}
		if seenCalls[e.CallID] {
			t.Fatalf("duplicate call id %q in published events", e.CallID)
		}
		seenCalls[e.CallID] = true
	}

	if got := len(bus.List()); got != n {
		t.Fatalf("List() = %d entries, want %d", got, n)
	}

	// Per-row partial answers: approving one row must not disturb the
	// others (plan.md — no all-or-nothing coupling).
	if err := bus.Resolve("sess", "call-a", ConfirmDecision{Approved: true}); err != nil {
		t.Fatalf("Resolve row: %v", err)
	}
	if r := <-results[0]; !r.decision.Approved {
		t.Fatal("approved row did not come back approved")
	}

	if got := bus.CancelBatch("batch-shared", "window closed"); got != n-1 {
		t.Fatalf("CancelBatch cancelled %d rows, want %d", got, n-1)
	}
	for i := 1; i < n; i++ {
		r := <-results[i]
		if r.err != nil {
			t.Fatalf("row %d err = %v", i, r.err)
		}
		if r.decision.Approved {
			t.Fatalf("row %d approved after CancelBatch — window close must deny", i)
		}
	}
	if n := bus.PendingCount(); n != 0 {
		t.Fatalf("PendingCount = %d after batch cancel, want 0", n)
	}
}

// One session's parked call must not block or resolve another's (FR-002).
func TestConfirmBus_UnrelatedSessionsUnaffected(t *testing.T) {
	t.Parallel()

	bus := NewConfirmBus(nil)
	ctx := context.Background()

	// Same call ID in two sessions: the key is the (session, call) pair.
	a := park(t, bus, ctx, req("sess-a", "call-1", "batch-a", "tool"))
	outB := make(chan awaitResult, 1)
	go func() {
		d, err := bus.Pending(ctx, req("sess-b", "call-1", "batch-b", "tool"))
		outB <- awaitResult{decision: d, err: err}
	}()
	waitForPending(t, bus, 2)

	if err := bus.Resolve("sess-a", "call-1", ConfirmDecision{Approved: true}); err != nil {
		t.Fatalf("Resolve sess-a: %v", err)
	}
	if r := <-a; !r.decision.Approved {
		t.Fatal("sess-a decision not delivered")
	}

	select {
	case r := <-outB:
		t.Fatalf("sess-b resolved by sess-a's answer: %+v", r)
	case <-time.After(50 * time.Millisecond):
	}

	if got := bus.ListSession("sess-b"); len(got) != 1 || got[0].SessionID != "sess-b" {
		t.Fatalf("ListSession(sess-b) = %+v, want one sess-b entry", got)
	}
	if got := bus.ListSession("sess-a"); len(got) != 0 {
		t.Fatalf("ListSession(sess-a) = %+v, want empty after resolve", got)
	}

	if err := bus.Cancel("sess-b", "call-1", ""); err != nil {
		t.Fatalf("Cancel sess-b: %v", err)
	}
	if r := <-outB; r.decision.Approved {
		t.Fatal("sess-b cancel produced approval")
	}
}

// Cancelling the run's context unblocks the parked call and unparks the
// entry so nothing leaks.
func TestConfirmBus_ContextCancellationUnblocks(t *testing.T) {
	t.Parallel()

	bus := NewConfirmBus(nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := park(t, bus, ctx, req("sess", "call-1", "batch-1", "tool"))

	cancel()

	select {
	case r := <-done:
		if !errors.Is(r.err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", r.err)
		}
		if r.decision.Approved {
			t.Fatal("cancelled wait produced an approval")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Pending did not unblock on context cancellation")
	}

	if n := bus.PendingCount(); n != 0 {
		t.Fatalf("PendingCount = %d after cancellation, want 0 (entry leaked)", n)
	}
}

// The bus holds a parked call for as long as the caller is willing to
// wait.
//
// This is the behavioural half of the no-deadline guarantee, and it is
// deliberately NOT the whole guarantee. A wait-and-see test can only
// prove "no timeout shorter than the window I was willing to sit
// through": it passes against a 250ms window and a 10-minute deadline
// alike, which is precisely the shape of gate this mission exists to
// stop shipping. TestConfirm_SourceHasNoDeadlinePrimitives is the half
// that can actually fail.
func TestConfirmBus_NoDeadline_ParksIndefinitely(t *testing.T) {
	t.Parallel()

	bus := NewConfirmBus(nil)
	done := park(t, bus, context.Background(), req("sess", "call-1", "batch-1", "tool"))

	select {
	case r := <-done:
		t.Fatalf("parked call resolved on its own after no answer: %+v — a timeout has crept in", r)
	case <-time.After(250 * time.Millisecond):
	}
	if n := bus.PendingCount(); n != 1 {
		t.Fatalf("PendingCount = %d, want 1 (still parked)", n)
	}
	_ = bus.Cancel("sess", "call-1", "test teardown")
	<-done
}

// deadlinePrimitives are the standard-library ways to convert elapsed
// time into an outcome. None of them belongs in confirm.go.
var deadlinePrimitives = []string{
	"time.After(",
	"time.NewTimer(",
	"time.AfterFunc(",
	"time.Tick(",
	"time.NewTicker(",
	"context.WithTimeout(",
	"context.WithDeadline(",
	"SetDeadline(",
}

// Owner decision 1 says elapsed time resolves to nothing: an unanswered
// confirmation parks the run until the user answers, the batch is
// cancelled, or the run's context dies. There is no clock in the pause.
//
// This test reads confirm.go and fails if any deadline primitive appears
// in it. That is a blunt instrument on purpose. The behavioural test
// above cannot distinguish "no deadline" from "a deadline longer than
// the test was willing to wait for" — and a ten-minute timeout added in
// good faith would sail through it while quietly converting the pause
// back into an await, which is the exact regression the decision
// forbids. Reading the source is the only assertion that fails on the
// day the deadline is introduced rather than the day someone waits long
// enough to notice.
//
// If a legitimate future need arises (the 01PMGX01 Phase 3 AskBus
// convergence, say), the fix is to change the decision in the spec and
// this test with it — not to route around it.
func TestConfirm_SourceHasNoDeadlinePrimitives(t *testing.T) {
	t.Parallel()

	const path = "confirm.go"
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v — the no-deadline guarantee is unverifiable without it", path, err)
	}
	body := string(src)
	// Strip line comments so prose that NAMES the forbidden primitives
	// (this file's own doc comment does) cannot trip the scan.
	var code strings.Builder
	for _, line := range strings.Split(body, "\n") {
		if idx := strings.Index(line, "//"); idx >= 0 {
			line = line[:idx]
		}
		code.WriteString(line)
		code.WriteString("\n")
	}
	stripped := code.String()

	for _, prim := range deadlinePrimitives {
		if strings.Contains(stripped, prim) {
			t.Errorf("%s uses %s — owner decision 1 forbids a clock in the pause primitive; "+
				"elapsed time must resolve to nothing", path, prim)
		}
	}

	// Guard the guard: if confirm.go is ever renamed or the scan starts
	// reading an empty file, an all-clear result would be a lie.
	if len(strings.TrimSpace(stripped)) < 1000 {
		t.Fatalf("%s scanned to %d bytes of code — the source scan is not reading the real file",
			path, len(strings.TrimSpace(stripped)))
	}
	if !strings.Contains(stripped, "func (b *ConfirmBus) Pending(") {
		t.Fatalf("%s does not contain ConfirmBus.Pending — the source scan is reading the wrong file", path)
	}
}

// Resolving something nobody is waiting on is an error, not a panic — a
// stale frontend must not corrupt the registry.
func TestConfirmBus_ResolveUnknown(t *testing.T) {
	t.Parallel()

	bus := NewConfirmBus(nil)
	if err := bus.Resolve("sess", "nope", ConfirmDecision{Approved: true}); !errors.Is(err, ErrUnknownConfirmation) {
		t.Fatalf("Resolve(unknown) = %v, want ErrUnknownConfirmation", err)
	}
	if err := bus.Cancel("sess", "nope", ""); !errors.Is(err, ErrUnknownConfirmation) {
		t.Fatalf("Cancel(unknown) = %v, want ErrUnknownConfirmation", err)
	}
	if got := bus.CancelBatch("no-such-batch", ""); got != 0 {
		t.Fatalf("CancelBatch(unknown) = %d, want 0", got)
	}
	if got := bus.List(); got != nil {
		t.Fatalf("List() on empty bus = %+v, want nil", got)
	}
}

func TestConfirmBus_DuplicateCallID(t *testing.T) {
	t.Parallel()

	bus := NewConfirmBus(nil)
	ctx := context.Background()
	done := park(t, bus, ctx, req("sess", "call-1", "batch-1", "tool"))

	if _, err := bus.Pending(ctx, req("sess", "call-1", "batch-1", "tool")); !errors.Is(err, ErrDuplicateConfirmation) {
		t.Fatalf("duplicate Pending = %v, want ErrDuplicateConfirmation", err)
	}
	if _, err := bus.Pending(ctx, ConfirmRequest{SessionID: "sess"}); err == nil {
		t.Fatal("Pending with empty CallID should error")
	}

	_ = bus.Cancel("sess", "call-1", "teardown")
	<-done
}

// Concurrent parks and resolves must be race-clean: the toolloop fans
// dispatches out across a worker pool.
func TestConfirmBus_ConcurrentParkResolve(t *testing.T) {
	t.Parallel()

	pub := &recordingPublisher{}
	bus := NewConfirmBus(pub.publish)
	ctx := context.Background()

	const n = 16
	var wg sync.WaitGroup
	approved := make(chan bool, n)
	for i := 0; i < n; i++ {
		id := NewConfirmID("call")
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := bus.Pending(ctx, req("sess", id, "batch", "tool"))
			if err != nil {
				t.Errorf("Pending: %v", err)
				return
			}
			approved <- d.Approved
		}()
		go func() {
			for {
				if err := bus.Resolve("sess", id, ConfirmDecision{Approved: true}); err == nil {
					return
				}
				time.Sleep(time.Millisecond)
			}
		}()
	}
	wg.Wait()
	close(approved)

	count := 0
	for a := range approved {
		if !a {
			t.Fatal("a concurrently-resolved call came back denied")
		}
		count++
	}
	if count != n {
		t.Fatalf("resolved %d calls, want %d", count, n)
	}
	if got := len(pub.snapshot()); got != n {
		t.Fatalf("published %d events, want %d", got, n)
	}
}

// The args summary is structural: names and kinds, never values. This is
// the redaction contract documented on TopicToolConfirmPending.
func TestSummarizeArgs_NeverLeaksValues(t *testing.T) {
	t.Parallel()

	if got := SummarizeArgs(nil); got != "no arguments" {
		t.Fatalf("SummarizeArgs(nil) = %q", got)
	}
	if got := SummarizeArgs(map[string]any{}); got != "no arguments" {
		t.Fatalf("SummarizeArgs(empty) = %q", got)
	}
	if got := SummarizeArgs(map[string]any{"path": "/etc/shadow"}); got != "1 argument: path (string)" {
		t.Fatalf("SummarizeArgs(one) = %q", got)
	}

	secret := "hunter2-super-secret"
	args := map[string]any{
		"token":    secret,
		"retries":  float64(3),
		"force":    true,
		"headers":  map[string]any{"authorization": "Bearer " + secret},
		"paths":    []any{"/etc/shadow"},
		"fallback": nil,
		"aardvark": "zzz",
	}
	got := SummarizeArgs(args)

	if strings.Contains(got, secret) || strings.Contains(got, "/etc/shadow") ||
		strings.Contains(got, "Bearer") || strings.Contains(got, "zzz") {
		t.Fatalf("summary leaked an argument value: %q", got)
	}
	want := "7 arguments: aardvark (string), fallback (null), force (boolean), " +
		"headers (object), paths (array), retries (number), token (string)"
	if got != want {
		t.Fatalf("SummarizeArgs =\n  %q\nwant\n  %q", got, want)
	}

	// Stable across calls regardless of map iteration order.
	for i := 0; i < 20; i++ {
		if SummarizeArgs(args) != want {
			t.Fatal("SummarizeArgs is not deterministic")
		}
	}
}

func TestConfirmBatchContext(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	if got := ConfirmBatchFromContext(ctx); got != "" {
		t.Fatalf("bare context batch = %q, want empty", got)
	}
	//nolint:staticcheck // deliberately checking the nil-context guard
	if got := ConfirmBatchFromContext(nil); got != "" {
		t.Fatalf("nil context batch = %q, want empty", got)
	}
	if got := ConfirmBatchFromContext(WithConfirmBatch(ctx, "")); got != "" {
		t.Fatalf("empty batch id should be a no-op, got %q", got)
	}
	if got := ConfirmBatchFromContext(WithConfirmBatch(ctx, "batch-7")); got != "batch-7" {
		t.Fatalf("batch = %q, want batch-7", got)
	}
}

func TestNewConfirmID_Unique(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id := NewConfirmID("confirm")
		if !strings.HasPrefix(id, "confirm-") {
			t.Fatalf("id %q lacks the prefix", id)
		}
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
}

// A nil bus must not panic — the adapter checks for nil before calling,
// but defensive nil-tolerance keeps a wiring mistake from crashing a run.
func TestConfirmBus_NilReceiver(t *testing.T) {
	t.Parallel()

	var bus *ConfirmBus
	if _, err := bus.Pending(context.Background(), req("s", "c", "b", "t")); err == nil {
		t.Fatal("nil bus Pending should error")
	}
	if err := bus.Resolve("s", "c", ConfirmDecision{}); !errors.Is(err, ErrUnknownConfirmation) {
		t.Fatalf("nil bus Resolve = %v", err)
	}
	if got := bus.CancelBatch("b", ""); got != 0 {
		t.Fatalf("nil bus CancelBatch = %d", got)
	}
	if got := bus.List(); got != nil {
		t.Fatalf("nil bus List = %+v", got)
	}
	if got := bus.PendingCount(); got != 0 {
		t.Fatalf("nil bus PendingCount = %d", got)
	}
}
