package main

import (
	"bufio"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- Test helpers ---

// startTestServer spins up a harness-vm listener on a random port and
// returns a closer and the address. token may be "" to disable auth checking.
// An optional ledger emitter may be supplied to exercise ledger emission;
// when omitted, connections run with ledger emission disabled (nil).
func startTestServer(t *testing.T, token string, ledger ...*ledgerEmitter) (net.Listener, string) {
	t.Helper()
	return startTestServerAudit(t, token, nil, ledger...)
}

// startTestServerAudit is startTestServer with an optional audit sink wired in.
// When audit is nil, audit emission is disabled (the no-op path). The stub
// executor keeps the offline echo behaviour (real execution is covered by
// agentexec_test.go with a fake adapter).
func startTestServerAudit(t *testing.T, token string, audit *auditSink, ledger ...*ledgerEmitter) (net.Listener, string) {
	t.Helper()
	var le *ledgerEmitter
	if len(ledger) > 0 {
		le = ledger[0]
	}
	return startTestServerFull(t, token, stubExecutor{}, audit, le)
}

// startTestServerWith is startTestServer with an explicit agent executor
// (agentexec_test.go's real-mode wire tests).
func startTestServerWith(t *testing.T, token string, exec agentExecutor) (net.Listener, string) {
	t.Helper()
	return startTestServerFull(t, token, exec, nil, nil)
}

// startTestServerFull is the shared accept loop behind the helpers above.
func startTestServerFull(t *testing.T, token string, exec agentExecutor, audit *auditSink, le *ledgerEmitter) (net.Listener, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("startTestServer: listen: %v", err)
	}
	log := newTestLogger()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// Task-surface tests don't exercise the read RPCs; a disabled
			// read service (nil api) answers any read kind with code:"unavailable".
			go handleConn(log, conn, token, exec, le, audit, &readService{log: log})
		}
	}()
	return ln, ln.Addr().String()
}

// newTestLogger returns a no-op slog logger for tests.
func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// dialAndAuth opens a TCP connection to addr and performs the auth handshake.
// If token == "", sends an empty token (works when server has no token configured).
func dialAndAuth(t *testing.T, addr, token string) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dialAndAuth: dial: %v", err)
	}
	sendMsg(t, conn, map[string]any{"kind": "auth", "token": token})
	resp := recvMsg(t, conn)
	if resp["kind"] != "auth.ok" {
		t.Fatalf("dialAndAuth: expected auth.ok; got %v", resp)
	}
	return conn
}

// sendMsg encodes m as NDJSON and writes it to conn.
func sendMsg(t *testing.T, conn net.Conn, m map[string]any) {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("sendMsg: marshal: %v", err)
	}
	b = append(b, '\n')
	if _, err := conn.Write(b); err != nil {
		t.Fatalf("sendMsg: write: %v", err)
	}
}

// recvMsg reads one NDJSON line from conn (blocking, with 3s deadline).
func recvMsg(t *testing.T, conn net.Conn) map[string]any {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }()
	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		t.Fatalf("recvMsg: no message received (connection closed or timeout)")
	}
	var m map[string]any
	if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
		t.Fatalf("recvMsg: unmarshal: %v", err)
	}
	return m
}

// readMessages spawns a goroutine that collects all messages from conn into a
// mutex-protected slice. Returns a pointer to the slice, its mutex, and a
// stop function.
//
// This is the race-safe replacement for the naive version in the BRIEF that
// appended to a slice without synchronisation (data race on the slice header).
func readMessages(t *testing.T, conn net.Conn) (*[]map[string]any, *sync.Mutex, func()) {
	t.Helper()
	var mu sync.Mutex
	msgs := make([]map[string]any, 0)
	stop := func() { _ = conn.Close() }
	go func() {
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			var m map[string]any
			if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
				continue
			}
			mu.Lock()
			msgs = append(msgs, m)
			mu.Unlock()
		}
	}()
	return &msgs, &mu, stop
}

// findKind returns the first message with kind==k, or nil. Caller must hold mu.
func findKind(msgs []map[string]any, k string) map[string]any {
	for _, m := range msgs {
		if m["kind"] == k {
			return m
		}
	}
	return nil
}

// waitForKind polls msgs (locking mu each time) until kind k appears or deadline.
func waitForKind(t *testing.T, msgs *[]map[string]any, mu *sync.Mutex, k string, timeout time.Duration) map[string]any {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			mu.Lock()
			got := make([]map[string]any, len(*msgs))
			copy(got, *msgs)
			mu.Unlock()
			t.Fatalf("waitForKind: timed out waiting for %q; got %v", k, got)
		}
		mu.Lock()
		found := findKind(*msgs, k)
		mu.Unlock()
		if found != nil {
			return found
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// --- Tests ---

// TestRPCHandshake verifies auth.ok is received after a valid auth message.
func TestRPCHandshake(t *testing.T) {
	srv, addr := startTestServer(t, "")
	defer srv.Close()

	conn := dialAndAuth(t, addr, "")
	conn.Close()
}

// TestAuthRequired verifies that the server closes the connection if the first
// message is not an auth message.
func TestAuthRequired(t *testing.T) {
	srv, addr := startTestServer(t, "secret-token")
	defer srv.Close()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Send task.start without auth first.
	sendMsg(t, conn, map[string]any{
		"kind":    "task.start",
		"task_id": "x",
	})

	resp := recvMsg(t, conn)
	if resp["kind"] != "auth.error" {
		t.Fatalf("expected auth.error; got %v", resp)
	}
}

// TestAuthSuccess verifies that a correct token produces auth.ok.
func TestAuthSuccess(t *testing.T) {
	srv, addr := startTestServer(t, "my-token")
	defer srv.Close()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	sendMsg(t, conn, map[string]any{"kind": "auth", "token": "my-token"})
	resp := recvMsg(t, conn)
	if resp["kind"] != "auth.ok" {
		t.Fatalf("expected auth.ok; got %v", resp)
	}
}

// TestAuthRejectsWrongToken verifies that when the server has a token
// configured (the baked-image / production path), a connection presenting the
// WRONG token in the auth handshake is rejected with auth.error and cannot
// proceed to send task messages. This is the deny-by-default contract that
// closes the optional-auth concern from Phase A (#163).
func TestAuthRejectsWrongToken(t *testing.T) {
	srv, addr := startTestServer(t, "correct-token")
	defer srv.Close()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Present a valid auth message but with the wrong token.
	sendMsg(t, conn, map[string]any{"kind": "auth", "token": "wrong-token"})
	resp := recvMsg(t, conn)
	if resp["kind"] != "auth.error" {
		t.Fatalf("expected auth.error for wrong token; got %v", resp)
	}
	// The error must not echo the configured token back.
	if mt, _ := resp["message_truncated"].(string); strings.Contains(mt, "correct-token") {
		t.Fatalf("auth.error leaked the configured token: %v", resp)
	}
}

// TestAuthRejectsMissingToken verifies that when the server has a token
// configured, an auth message with NO token field (empty client token) is
// rejected. An empty client token must never satisfy a non-empty server token.
func TestAuthRejectsMissingToken(t *testing.T) {
	srv, addr := startTestServer(t, "correct-token")
	defer srv.Close()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// auth message with no token field at all.
	sendMsg(t, conn, map[string]any{"kind": "auth"})
	resp := recvMsg(t, conn)
	if resp["kind"] != "auth.error" {
		t.Fatalf("expected auth.error for missing token; got %v", resp)
	}
}

// TestAuthRequiredRejectsTaskUntilAuthed verifies the handshake is REQUIRED: a
// connection that fails auth (wrong token) cannot drive a task even if it then
// sends a well-formed task.start — the server closed it after auth.error.
func TestAuthRequiredRejectsTaskUntilAuthed(t *testing.T) {
	srv, addr := startTestServer(t, "correct-token")
	defer srv.Close()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	sendMsg(t, conn, map[string]any{"kind": "auth", "token": "nope"})
	resp := recvMsg(t, conn)
	if resp["kind"] != "auth.error" {
		t.Fatalf("expected auth.error; got %v", resp)
	}

	// After auth.error the server closes the conn. A subsequent task.start must
	// NOT be serviced: the next read returns EOF (connection closed), not a
	// task lifecycle event.
	sendMsg(t, conn, map[string]any{"kind": "task.start", "task_id": "x", "prompt": "p"})
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	scanner := bufio.NewScanner(conn)
	if scanner.Scan() {
		t.Fatalf("expected connection closed after auth.error; got line %q", scanner.Text())
	}
}

// TestAuthDevModeUnauthenticated verifies the local-dev path: when the server
// has NO token configured (empty HARNESS_VM_TOKEN), any auth handshake — even
// one carrying a spurious token — is accepted. This preserves the documented
// dev-mode escape hatch while production (token set) stays deny-by-default.
func TestAuthDevModeUnauthenticated(t *testing.T) {
	srv, addr := startTestServer(t, "")
	defer srv.Close()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// A token is present in the message but the server has none configured.
	sendMsg(t, conn, map[string]any{"kind": "auth", "token": "ignored-in-dev"})
	resp := recvMsg(t, conn)
	if resp["kind"] != "auth.ok" {
		t.Fatalf("dev mode (no server token) must accept any handshake; got %v", resp)
	}
}

// TestBadRequest verifies that an unknown message kind after auth gets a
// task.error{code:bad_request} response.
func TestBadRequest(t *testing.T) {
	srv, addr := startTestServer(t, "")
	defer srv.Close()

	conn := dialAndAuth(t, addr, "")
	defer conn.Close()

	sendMsg(t, conn, map[string]any{"kind": "bogus.message"})
	resp := recvMsg(t, conn)
	if resp["kind"] != "task.error" {
		t.Fatalf("expected task.error; got %v", resp)
	}
	if resp["code"] != "bad_request" {
		t.Fatalf("expected code bad_request; got %v", resp["code"])
	}
}

// TestErrorMsgTruncation verifies that an extremely long kind string results in
// a truncated message_truncated field (≤ maxMessageLen+3 for the "..." suffix).
func TestErrorMsgTruncation(t *testing.T) {
	srv, addr := startTestServer(t, "")
	defer srv.Close()

	conn := dialAndAuth(t, addr, "")
	defer conn.Close()

	// Send a kind value that is very long.
	longKind := strings.Repeat("x", 512)
	sendMsg(t, conn, map[string]any{"kind": longKind})
	resp := recvMsg(t, conn)
	if resp["kind"] != "task.error" {
		t.Fatalf("expected task.error; got %v", resp)
	}
	msgT, _ := resp["message_truncated"].(string)
	if len([]rune(msgT)) > maxMessageLen+3 {
		t.Fatalf("message_truncated too long: %d runes", len([]rune(msgT)))
	}
}

// TestCancelUnknownTaskIsNoop verifies that cancelling a task_id when no task
// is running does not produce any response (noop).
func TestCancelUnknownTaskIsNoop(t *testing.T) {
	srv, addr := startTestServer(t, "")
	defer srv.Close()

	conn := dialAndAuth(t, addr, "")
	defer conn.Close()

	msgs, mu, stop := readMessages(t, conn)
	defer stop()

	sendMsg(t, conn, map[string]any{
		"kind":    "task.cancel",
		"task_id": "nonexistent-task",
	})

	// Wait a bit; no response should arrive.
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	n := len(*msgs)
	mu.Unlock()
	if n != 0 {
		mu.Lock()
		got := make([]map[string]any, len(*msgs))
		copy(got, *msgs)
		mu.Unlock()
		t.Fatalf("expected no response for cancel of unknown task; got %v", got)
	}
}

// TestTaskConflict verifies that starting a second task while one is already
// running returns task.error{code:task_conflict} (Bug 2).
func TestTaskConflict(t *testing.T) {
	srv, addr := startTestServer(t, "")
	defer srv.Close()

	conn := dialAndAuth(t, addr, "")
	defer conn.Close()

	msgs, mu, stop := readMessages(t, conn)
	defer stop()

	// Start first task.
	sendMsg(t, conn, map[string]any{
		"kind":    "task.start",
		"task_id": "task-a",
		"prompt":  "first task",
	})

	// Immediately start second task — should get conflict.
	sendMsg(t, conn, map[string]any{
		"kind":    "task.start",
		"task_id": "task-b",
		"prompt":  "second task (conflict)",
	})

	// Wait for task.error with code task_conflict.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if time.Now().After(deadline) {
			mu.Lock()
			got := make([]map[string]any, len(*msgs))
			copy(got, *msgs)
			mu.Unlock()
			t.Fatalf("expected task.error for conflict; got %v", got)
		}
		mu.Lock()
		var found map[string]any
		for _, m := range *msgs {
			if m["kind"] == "task.error" && m["code"] == "task_conflict" {
				found = m
				break
			}
		}
		mu.Unlock()
		if found != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestMultipleTasksOnConnection verifies that after task A completes, task B
// can start and complete on the same connection (Bug 3).
func TestMultipleTasksOnConnection(t *testing.T) {
	srv, addr := startTestServer(t, "")
	defer srv.Close()

	conn := dialAndAuth(t, addr, "")
	defer conn.Close()

	msgs, mu, stop := readMessages(t, conn)
	defer stop()

	// Start task A.
	sendMsg(t, conn, map[string]any{
		"kind":    "task.start",
		"task_id": "task-a",
		"prompt":  "first",
	})

	// Wait for task A to complete.
	waitForKind(t, msgs, mu, "task.complete", 3*time.Second)

	// Start task B on the same connection.
	sendMsg(t, conn, map[string]any{
		"kind":    "task.start",
		"task_id": "task-b",
		"prompt":  "second",
	})

	// Wait for task B to complete.
	deadline := time.Now().Add(3 * time.Second)
	for {
		if time.Now().After(deadline) {
			mu.Lock()
			got := make([]map[string]any, len(*msgs))
			copy(got, *msgs)
			mu.Unlock()
			t.Fatalf("second task did not complete; msgs: %v", got)
		}
		mu.Lock()
		var completions int
		for _, m := range *msgs {
			if m["kind"] == "task.complete" {
				completions++
			}
		}
		mu.Unlock()
		if completions >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
}
