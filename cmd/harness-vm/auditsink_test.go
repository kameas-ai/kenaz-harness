package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"sort"
	"sync"
	"testing"
	"time"
)

// fakeAuditSock is a test double for the reporter's audit collector socket. It
// listens on TCP (the sink's network is overridden to "tcp" in tests) and
// collects every line-delimited JSON record written to it.
//
// DETERMINISM: the audit sink dials a NEW connection per record and emits
// synchronously on the caller's goroutine, so dial order == emit order. But
// each accepted connection is serviced by its own goroutine, so the order in
// which records are *appended* to a shared slice is scheduler-dependent. To
// make ordering assertions stable under -race, accept assigns each connection
// a monotonic sequence number (under the accept loop, which is single-threaded)
// and records are returned sorted by that sequence — recovering the
// deterministic emit order regardless of handler-goroutine scheduling.
type fakeAuditSock struct {
	ln   net.Listener
	mu   sync.Mutex
	recs []seqRecord
}

// seqRecord pairs an audit record with the accept-sequence of the connection it
// arrived on, so the test can recover deterministic emit order.
type seqRecord struct {
	seq int
	rec auditRecord
}

func newFakeAuditSock(t *testing.T) *fakeAuditSock {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("newFakeAuditSock: listen: %v", err)
	}
	f := &fakeAuditSock{ln: ln}
	go func() {
		seq := 0
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// Sequence is assigned in the single-threaded accept loop, so it
			// reflects dial order (== synchronous emit order) deterministically.
			seq++
			go f.handle(conn, seq)
		}
	}()
	return f
}

func (f *fakeAuditSock) handle(conn net.Conn, seq int) {
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		var rec auditRecord
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			continue
		}
		f.mu.Lock()
		f.recs = append(f.recs, seqRecord{seq: seq, rec: rec})
		f.mu.Unlock()
	}
}

func (f *fakeAuditSock) addr() string { return f.ln.Addr().String() }
func (f *fakeAuditSock) close()       { _ = f.ln.Close() }

// snapshot returns the collected records sorted by accept sequence, recovering
// deterministic emit order regardless of handler-goroutine scheduling.
func (f *fakeAuditSock) snapshot() []auditRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	srt := make([]seqRecord, len(f.recs))
	copy(srt, f.recs)
	sort.SliceStable(srt, func(i, j int) bool { return srt[i].seq < srt[j].seq })
	out := make([]auditRecord, len(srt))
	for i := range srt {
		out[i] = srt[i].rec
	}
	return out
}

// waitForKinds polls until at least n records have arrived or the deadline
// elapses, then returns the snapshot.
func (f *fakeAuditSock) waitForCount(t *testing.T, n int, timeout time.Duration) []auditRecord {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		snap := f.snapshot()
		if len(snap) >= n {
			return snap
		}
		if time.Now().After(deadline) {
			t.Fatalf("waitForCount: timed out waiting for %d records; saw %d (%v)", n, len(snap), snap)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func newTCPAuditSink(addr string) *auditSink {
	return newAuditSink(addr, "tcp", newTestLogger())
}

// TestAuditSinkFourLifecycleLines is the core deliverable test: a fake-model
// graph run (the offline transform graph) drives the four audit lines —
// task.start, task.tool_call, task.tool_result, task.complete — to the sink,
// in order, metadata-only.
func TestAuditSinkFourLifecycleLines(t *testing.T) {
	sock := newFakeAuditSock(t)
	defer sock.close()

	sink := newTCPAuditSink(sock.addr())

	const taskID = "task-audit-1"
	const secret = "summarize the confidential merger memo"

	// Drive the full lifecycle the way runTask does: task.start, the graph run
	// (which emits tool_call/tool_result per node via the tracer), task.complete.
	started := time.Now()
	sink.emitTaskStart(taskID)
	tracer := newNodeTracer(sink, taskID)
	err := runAgentTaskGraph(context.Background(), taskID, secret, stubExecutor{}, tracer, func(_, _ string) {})
	if err != nil {
		t.Fatalf("runAgentTaskGraph: %v", err)
	}
	sink.emitTaskComplete(taskID, 0, time.Since(started).Milliseconds())

	// Two-node graph ⇒ at least: start + (tool_call,tool_result)x2 + complete = 6.
	recs := sock.waitForCount(t, 6, 3*time.Second)

	// --- All four kinds present, and the ordering invariant holds. ---
	var sawStart, sawComplete bool
	var firstKind, lastKind string
	kinds := map[string]int{}
	for i, r := range recs {
		kinds[r.Kind]++
		if i == 0 {
			firstKind = r.Kind
		}
		lastKind = r.Kind
		if r.Kind == auditKindTaskStart {
			sawStart = true
		}
		if r.Kind == auditKindTaskComplete {
			sawComplete = true
		}
		// Every record carries the task id and a millisecond timestamp.
		if r.TaskID != taskID {
			t.Fatalf("record %d not tagged with task id: %+v", i, r)
		}
		if r.TS <= 0 {
			t.Fatalf("record %d missing timestamp: %+v", i, r)
		}
	}
	if !sawStart || !sawComplete {
		t.Fatalf("missing terminal kinds: start=%v complete=%v (%v)", sawStart, sawComplete, recs)
	}
	if kinds[auditKindToolCall] < 1 || kinds[auditKindToolResult] < 1 {
		t.Fatalf("expected >=1 tool_call and >=1 tool_result; got %v", kinds)
	}
	if kinds[auditKindToolCall] != kinds[auditKindToolResult] {
		t.Fatalf("tool_call/tool_result mismatch (each call must have a result): %v", kinds)
	}
	// task.start must be first; task.complete must be last.
	if firstKind != auditKindTaskStart {
		t.Fatalf("first audit line must be task.start; got %q", firstKind)
	}
	if lastKind != auditKindTaskComplete {
		t.Fatalf("last audit line must be task.complete; got %q", lastKind)
	}
	// Each tool_call must precede its matching tool_result.
	assertCallBeforeResult(t, recs)

	// --- HARD PRIVACY GATE: no record may carry prompt text or any content. ---
	for _, r := range recs {
		raw, _ := json.Marshal(r)
		if containsSubstr(string(raw), secret) {
			t.Fatalf("audit record leaked prompt text: %s", raw)
		}
		// tool/node are structural identifiers only; assert they are not the
		// prompt (a regression guard against accidentally piping content in).
		if r.Tool == secret || r.Node == secret {
			t.Fatalf("audit record put content in tool/node field: %+v", r)
		}
	}
}

// assertCallBeforeResult checks that for the sequence of tool_call/tool_result
// records, each result is preceded by a call (balanced, call-first).
func assertCallBeforeResult(t *testing.T, recs []auditRecord) {
	t.Helper()
	open := 0
	for _, r := range recs {
		switch r.Kind {
		case auditKindToolCall:
			open++
		case auditKindToolResult:
			if open <= 0 {
				t.Fatalf("tool_result with no preceding tool_call: %v", recs)
			}
			open--
		}
	}
	if open != 0 {
		t.Fatalf("unbalanced tool_call/tool_result (open=%d): %v", open, recs)
	}
}

// TestAuditSinkDisabledIsNoop verifies an unconfigured sink (and a nil sink)
// never panic and never write — the dev / host-mode path.
func TestAuditSinkDisabledIsNoop(t *testing.T) {
	s := newAuditSink("", "unix", newTestLogger())
	if s.enabled() {
		t.Fatalf("empty addr must be disabled")
	}
	s.emitTaskStart("t")
	s.emitToolCall("t", "tool", "node")
	s.emitToolResult("t", "tool", "node", 0, 1)
	s.emitTaskComplete("t", 0, 1)

	var ns *auditSink
	if ns.enabled() {
		t.Fatalf("nil sink must report disabled")
	}
	ns.emitTaskStart("t")
	ns.emitTaskComplete("t", 0, 1)
}

// TestAuditSinkDialFailureDoesNotBlock verifies a dead audit socket does not
// block or panic the emit path — the record is dropped silently.
func TestAuditSinkDialFailureDoesNotBlock(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	s := newTCPAuditSink(addr)
	done := make(chan struct{})
	go func() {
		s.emitTaskStart("t")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("emit blocked on a dead audit socket")
	}
}

// TestAuditSinkCancelEmitsResult verifies that a cancelled graph run still
// closes out every started node with a tool_result (so the audit timeline is
// never left with a dangling tool_call).
func TestAuditSinkCancelEmitsResult(t *testing.T) {
	sock := newFakeAuditSock(t)
	defer sock.close()
	sink := newTCPAuditSink(sock.addr())

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel almost immediately so the run aborts during the first node.
	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()

	tracer := newNodeTracer(sink, "task-cancel-audit")
	_ = runAgentTaskGraph(ctx, "task-cancel-audit", "x", stubExecutor{}, tracer, func(_, _ string) {})

	// Give the per-record dials a moment to land.
	recs := sock.waitForCount(t, 2, 2*time.Second) // at least one call + its result
	assertCallBeforeResult(t, recs)
}
