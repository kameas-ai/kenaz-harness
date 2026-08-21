package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

// fakeIngest is a test double for the reporter ingest endpoint. It listens on
// a TCP socket (the emitter's network is overridden to "tcp" in tests) and
// collects every NDJSON record written to it, one record per connection.
type fakeIngest struct {
	ln   net.Listener
	mu   sync.Mutex
	recs []ledgerRecord
}

func newFakeIngest(t *testing.T) *fakeIngest {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("newFakeIngest: listen: %v", err)
	}
	f := &fakeIngest{ln: ln}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.handle(conn)
		}
	}()
	return f
}

func (f *fakeIngest) handle(conn net.Conn) {
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		var rec ledgerRecord
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			continue
		}
		f.mu.Lock()
		f.recs = append(f.recs, rec)
		f.mu.Unlock()
	}
}

func (f *fakeIngest) addr() string { return f.ln.Addr().String() }
func (f *fakeIngest) close()       { _ = f.ln.Close() }

// snapshot returns a copy of the records seen so far.
func (f *fakeIngest) snapshot() []ledgerRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]ledgerRecord, len(f.recs))
	copy(out, f.recs)
	return out
}

// waitForPhases polls until at least the named phases have all been seen, or
// the deadline elapses. Returns the snapshot at the moment of success.
func (f *fakeIngest) waitForPhases(t *testing.T, timeout time.Duration, phases ...string) []ledgerRecord {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		snap := f.snapshot()
		seen := map[string]bool{}
		for _, r := range snap {
			if p, _ := r.Payload["phase"].(string); p != "" {
				seen[p] = true
			}
		}
		all := true
		for _, p := range phases {
			if !seen[p] {
				all = false
				break
			}
		}
		if all {
			return snap
		}
		if time.Now().After(deadline) {
			t.Fatalf("waitForPhases: timed out waiting for %v; saw %v", phases, snap)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitForPhaseCount polls until at least n records carrying the given phase
// have been collected, or the deadline elapses. Returns the snapshot at the
// moment of success.
//
// This is the correct primitive for tests that expect MULTIPLE records of the
// same phase (e.g. N tool_call records for an N-step preset). waitForPhases
// exits as soon as it sees the first record of each listed phase; callers that
// rely on a multiset count must use waitForPhaseCount instead to avoid the
// race where the snapshot is read before all in-flight fakeIngest.handle
// goroutines have appended their records.
func (f *fakeIngest) waitForPhaseCount(t *testing.T, timeout time.Duration, phase string, n int) []ledgerRecord {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		snap := f.snapshot()
		count := 0
		for _, r := range snap {
			if p, _ := r.Payload["phase"].(string); p == phase {
				count++
			}
		}
		if count >= n {
			return snap
		}
		if time.Now().After(deadline) {
			t.Fatalf("waitForPhaseCount: timed out waiting for %d %q records; saw %d in %v", n, phase, count, snap)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func newTCPEmitter(addr, workbenchID string) *ledgerEmitter {
	e := newLedgerEmitter(addr, "tcp", workbenchID, newTestLogger())
	return e
}

// TestLedgerEmitTaskStart verifies a task.start record carries the workbench
// id, task id, and prompt length — and NOT the prompt text (privacy).
func TestLedgerEmitTaskStart(t *testing.T) {
	ingest := newFakeIngest(t)
	defer ingest.close()

	e := newTCPEmitter(ingest.addr(), "daily-driver-123")
	e.emitTaskStart("task-1", 42)

	recs := ingest.waitForPhases(t, 2*time.Second, phaseTaskStart)
	if len(recs) != 1 {
		t.Fatalf("expected 1 record; got %d (%v)", len(recs), recs)
	}
	r := recs[0]
	if r.Kind != ledgerEventKind {
		t.Fatalf("kind = %q; want %q", r.Kind, ledgerEventKind)
	}
	if r.Source != ledgerSource {
		t.Fatalf("source = %q; want %q", r.Source, ledgerSource)
	}
	if r.Payload["phase"] != phaseTaskStart {
		t.Fatalf("phase = %v; want %q", r.Payload["phase"], phaseTaskStart)
	}
	if r.Payload["task_id"] != "task-1" {
		t.Fatalf("task_id = %v; want task-1", r.Payload["task_id"])
	}
	if r.Payload["workbench_id"] != "daily-driver-123" {
		t.Fatalf("workbench_id = %v; want daily-driver-123", r.Payload["workbench_id"])
	}
	// prompt_len is a float64 after JSON round-trip.
	if pl, _ := r.Payload["prompt_len"].(float64); pl != 42 {
		t.Fatalf("prompt_len = %v; want 42", r.Payload["prompt_len"])
	}
	if r.Timestamp.IsZero() {
		t.Fatalf("timestamp must be set")
	}
}

// TestLedgerPrivacyNoPromptText is a hard privacy gate: no ledger record may
// ever contain a "prompt" field or the prompt text itself.
func TestLedgerPrivacyNoPromptText(t *testing.T) {
	ingest := newFakeIngest(t)
	defer ingest.close()

	e := newTCPEmitter(ingest.addr(), "wb-1")
	secret := "summarize the confidential merger memo"
	e.emitTaskStart("t", len(secret))

	recs := ingest.waitForPhases(t, 2*time.Second, phaseTaskStart)
	for _, r := range recs {
		if _, ok := r.Payload["prompt"]; ok {
			t.Fatalf("ledger leaked a prompt field: %v", r.Payload)
		}
		raw, _ := json.Marshal(r)
		if containsSubstr(string(raw), secret) {
			t.Fatalf("ledger leaked prompt text in record: %s", raw)
		}
	}
}

// TestLedgerToolCall verifies a tool_call record carries the tool name and not
// any arguments.
func TestLedgerToolCall(t *testing.T) {
	ingest := newFakeIngest(t)
	defer ingest.close()

	e := newTCPEmitter(ingest.addr(), "wb-1")
	e.emitToolCall("task-9", "browser.fetch")

	recs := ingest.waitForPhases(t, 2*time.Second, phaseToolCall)
	if len(recs) != 1 {
		t.Fatalf("expected 1 record; got %d", len(recs))
	}
	if recs[0].Payload["tool"] != "browser.fetch" {
		t.Fatalf("tool = %v; want browser.fetch", recs[0].Payload["tool"])
	}
}

// TestLedgerDisabledIsNoop verifies that an emitter with no socket configured
// (and a nil emitter) never panics and never writes.
func TestLedgerDisabledIsNoop(t *testing.T) {
	// Empty address → disabled.
	e := newLedgerEmitter("", "unix", "wb", newTestLogger())
	if e.enabled() {
		t.Fatalf("empty addr must be disabled")
	}
	e.emitTaskStart("t", 1)
	e.emitToolCall("t", "x")
	e.emitTaskComplete("t", 0)
	e.emitTaskCancelled("t")

	// nil receiver must be a safe no-op too.
	var ne *ledgerEmitter
	if ne.enabled() {
		t.Fatalf("nil emitter must report disabled")
	}
	ne.emitTaskStart("t", 1)
	ne.emitTaskComplete("t", 0)
}

// TestLedgerDialFailureDoesNotBlock verifies that a dead ingest endpoint does
// not block or panic the emit path — the record is dropped silently.
func TestLedgerDialFailureDoesNotBlock(t *testing.T) {
	// Reserve a port then close it so the dial fails fast.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	e := newTCPEmitter(addr, "wb-1")
	done := make(chan struct{})
	go func() {
		e.emitTaskStart("t", 1)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("emit blocked on a dead endpoint")
	}
}

// TestTaskLifecycleEmitsToLedger is the end-to-end test for criterion #5 /
// finding #11: a task dispatched through the RPC server produces the full
// lifecycle (task.start → tool_call → task.complete) on the ledger, all
// tagged with the configured workbench id.
func TestTaskLifecycleEmitsToLedger(t *testing.T) {
	ingest := newFakeIngest(t)
	defer ingest.close()

	ledger := newTCPEmitter(ingest.addr(), "daily-driver-777")
	srv, addr := startTestServer(t, "", ledger)
	defer srv.Close()

	conn := dialAndAuth(t, addr, "")
	defer conn.Close()

	msgs, mu, stop := readMessages(t, conn)
	defer stop()

	sendMsg(t, conn, map[string]any{
		"kind":    "task.start",
		"task_id": "task-e2e",
		"prompt":  "do the thing",
	})

	// Wait for the RPC stream to complete.
	waitForKind(t, msgs, mu, "task.complete", 3*time.Second)

	// Now assert the ledger saw the full lifecycle.
	recs := ingest.waitForPhases(t, 3*time.Second, phaseTaskStart, phaseToolCall, phaseTaskComplete)

	var start, complete int
	var tools int
	for _, r := range recs {
		if r.Payload["workbench_id"] != "daily-driver-777" {
			t.Fatalf("record not tagged with workbench id: %v", r.Payload)
		}
		if r.Payload["task_id"] != "task-e2e" {
			t.Fatalf("record not tagged with task id: %v", r.Payload)
		}
		switch r.Payload["phase"] {
		case phaseTaskStart:
			start++
		case phaseToolCall:
			tools++
		case phaseTaskComplete:
			complete++
		}
	}
	if start != 1 {
		t.Fatalf("expected exactly 1 task.start; got %d", start)
	}
	if complete != 1 {
		t.Fatalf("expected exactly 1 task.complete; got %d", complete)
	}
	if tools < 1 {
		t.Fatalf("expected >=1 tool_call; got %d", tools)
	}
}

// TestLedgerFailedTaskDiffersFromSuccess drives runTask to a graph-run
// failure and asserts the ledger's task.complete record differs from a
// successful run's — WP11 / AC-016. Before this WP, emitTaskComplete took no
// exit code, so main.go:566 (the failure path) and main.go:585 (the success
// path) wrote the byte-identical {"phase":"task.complete"} record. The audit
// sink already distinguished them (auditsink.go:67 ExitCode); the ledger did
// not, even though the two terminal calls sit one line apart.
func TestLedgerFailedTaskDiffersFromSuccess(t *testing.T) {
	ingest := newFakeIngest(t)
	defer ingest.close()

	ledger := newTCPEmitter(ingest.addr(), "wb-fail")
	srv, addr := startTestServerFull(t, "", failingExecutor{err: errors.New("boom")}, nil, ledger)
	defer srv.Close()

	conn := dialAndAuth(t, addr, "")
	defer conn.Close()

	msgs, mu, stop := readMessages(t, conn)
	defer stop()

	sendMsg(t, conn, map[string]any{
		"kind":    "task.start",
		"task_id": "task-fail",
		"prompt":  "do the thing",
	})

	// The RPC stream reports the failure as task.error (graph_run_failed);
	// the ledger record is a separate side channel written at the same call
	// site (main.go's runTask), so wait for both signals independently.
	waitForKind(t, msgs, mu, "task.error", 3*time.Second)
	recs := ingest.waitForPhases(t, 3*time.Second, phaseTaskComplete)

	var completeRec *ledgerRecord
	for i := range recs {
		if recs[i].Payload["phase"] == phaseTaskComplete && recs[i].Payload["task_id"] == "task-fail" {
			completeRec = &recs[i]
			break
		}
	}
	if completeRec == nil {
		t.Fatalf("no task.complete ledger record found for the failed task: %v", recs)
	}
	exitCode, ok := completeRec.Payload["exit_code"].(float64)
	if !ok {
		t.Fatalf("failed task's ledger record carries no exit_code key: %v", completeRec.Payload)
	}
	if exitCode == 0 {
		t.Fatalf("failed task's ledger record reports exit_code=0 — looks like a success record: %v", completeRec.Payload)
	}
}

// TestLedgerCancelRecordUnchanged pins AC-016's second half: WP11's exit_code
// addition is scoped to emitTaskComplete (task.complete) only. The cancel
// path already had its own correct terminal phase (phaseTaskCancelled,
// main.go's cancellation branch) before this WP and must not grow the new key.
func TestLedgerCancelRecordUnchanged(t *testing.T) {
	ingest := newFakeIngest(t)
	defer ingest.close()

	e := newTCPEmitter(ingest.addr(), "wb-cancel")
	e.emitTaskCancelled("task-c")

	recs := ingest.waitForPhases(t, 2*time.Second, phaseTaskCancelled)
	if len(recs) != 1 {
		t.Fatalf("expected 1 record; got %d", len(recs))
	}
	r := recs[0]
	if r.Payload["phase"] != phaseTaskCancelled {
		t.Fatalf("phase = %v; want %q", r.Payload["phase"], phaseTaskCancelled)
	}
	if _, ok := r.Payload["exit_code"]; ok {
		t.Fatalf("cancel record must not carry exit_code (scoped to task.complete only): %v", r.Payload)
	}
}

func containsSubstr(haystack, needle string) bool {
	if needle == "" {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
