// Command harness-vm is the in-VM kenaz-harness RPC service for Phase 8.
//
// It listens on a TCP port (standing in for a vsock listener — vsock
// framing deferred to a later phase), accepts long-lived per-workbench
// connections, and multiplexes task RPCs over each connection.
//
// Wire contract: see kenaz-harness/contracts/vm-rpc.md (Phase 8 section).
//
// Build with:
//
//	go build -o bin/kenaz-harness-vm ./cmd/harness-vm/
//
// Auth: client must send {"kind":"auth","token":"<HARNESS_VM_TOKEN>"}
// as the first message on each connection. Server replies
// {"kind":"auth.ok"} or closes the connection with {"kind":"auth.error"}.
//
// Task lifecycle (per connection, one active task at a time):
//
//	C→S: {"kind":"task.start","task_id":"<id>","prompt":"<prompt>"}
//	S→C: {"kind":"task.running","task_id":"<id>","text":"<chunk>"}  (0..N)
//	S→C: {"kind":"task.complete","task_id":"<id>"}                  (terminal)
//
// Cancel path:
//
//	C→S: {"kind":"task.cancel","task_id":"<id>"}
//	S→C: {"kind":"task.cancelled","task_id":"<id>"}                 (terminal)
//
// Conflict (second task.start while one is running):
//
//	S→C: {"kind":"task.error","task_id":"<id>","code":"task_conflict","message_truncated":"..."}
//
// Port: 7881 TCP (or $HARNESS_VM_PORT). Phase 9 upgrades to vsock.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const defaultHarnessPort = "7881"

// maxMessageLen caps the message_truncated field to this many runes.
const maxMessageLen = 64

func main() {
	addr := "0.0.0.0:" + defaultHarnessPort
	if p := os.Getenv("HARNESS_VM_PORT"); p != "" {
		addr = "0.0.0.0:" + p
	}

	token := os.Getenv("HARNESS_VM_TOKEN")

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Error("kenaz-harness-vm: listen failed", "addr", addr, "err", err)
		os.Exit(1)
	}
	log.Info("kenaz-harness-vm listening", "addr", addr)

	sigC := make(chan os.Signal, 1)
	signal.Notify(sigC, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigC
		log.Info("kenaz-harness-vm: shutting down")
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Info("kenaz-harness-vm: accept loop exiting", "reason", err)
			return
		}
		go handleConn(log, conn, token)
	}
}

// msg is a generic inbound or outbound message.
type msg map[string]any

// connWriter serialises concurrent writes to a net.Conn.
// handleConn and runTask goroutines both write to the same conn;
// unprotected concurrent writes corrupt the NDJSON stream.
type connWriter struct {
	mu   sync.Mutex
	conn net.Conn
}

func (w *connWriter) send(m msg) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	w.mu.Lock()
	_, err = w.conn.Write(b)
	w.mu.Unlock()
	return err
}

// handleConn manages the full lifecycle of one client connection:
// auth handshake, then a loop dispatching task messages.
func handleConn(log *slog.Logger, conn net.Conn, token string) {
	defer conn.Close()

	w := &connWriter{conn: conn}
	scanner := bufio.NewScanner(conn)

	// --- Auth handshake ---
	if !scanner.Scan() {
		return // closed before auth
	}
	var authMsg msg
	if err := json.Unmarshal(scanner.Bytes(), &authMsg); err != nil {
		_ = w.send(msg{"kind": "auth.error", "message_truncated": "bad json"})
		return
	}
	if authMsg["kind"] != "auth" {
		_ = w.send(msg{"kind": "auth.error", "message_truncated": "expected auth message"})
		return
	}
	// When a token is configured, validate it.
	if token != "" {
		clientToken, _ := authMsg["token"].(string)
		if clientToken != token {
			_ = w.send(msg{"kind": "auth.error", "message_truncated": "invalid token"})
			return
		}
	}
	if err := w.send(msg{"kind": "auth.ok"}); err != nil {
		return
	}

	// --- Per-connection session: one active task at a time ---
	//
	// busy == 1  → a task goroutine is running.
	// busy == 0  → connection is idle, ready for a new task.start.
	var busy atomic.Int32

	// cancelFn holds the cancel function for the currently-running task.
	// Access is safe: cancelFn is written by handleConn's scanner loop before
	// busy is set, and read only in the cancel branch of the same loop.
	// Since task.start and task.cancel are processed sequentially in the same
	// goroutine, no concurrent write can race with the cancel read.
	var cancelFn context.CancelFunc

	for scanner.Scan() {
		var m msg
		if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
			_ = w.send(msg{
				"kind":              "task.error",
				"code":              "bad_request",
				"message_truncated": truncate(fmt.Sprintf("json decode: %v", err), maxMessageLen),
			})
			continue
		}

		kind, _ := m["kind"].(string)
		switch kind {
		case "task.start":
			taskID, _ := m["task_id"].(string)
			if taskID == "" {
				_ = w.send(msg{
					"kind":              "task.error",
					"code":              "bad_request",
					"message_truncated": "task_id required",
				})
				continue
			}

			// Concurrent-task guard (Bug 2 fix).
			if !busy.CompareAndSwap(0, 1) {
				_ = w.send(msg{
					"kind":              "task.error",
					"task_id":           taskID,
					"code":              "task_conflict",
					"message_truncated": "a task is already running on this connection",
				})
				continue
			}

			prompt, _ := m["prompt"].(string)
			ctx, cancel := context.WithCancel(context.Background())
			cancelFn = cancel

			// Spawn the task runner. It writes via w and clears busy when done.
			go runTask(log, w, &busy, cancel, ctx, taskID, prompt)

		case "task.cancel":
			if busy.Load() == 0 {
				// No task running — silently ignore (noop, as tests expect).
				continue
			}
			// Signal cancellation. The running goroutine emits task.cancelled
			// and clears busy (Bug 1 + Bug 2 fix).
			if cancelFn != nil {
				cancelFn()
			}

		default:
			_ = w.send(msg{
				"kind":              "task.error",
				"code":              "bad_request",
				"message_truncated": truncate(fmt.Sprintf("unknown kind: %q", kind), maxMessageLen),
			})
		}
	}
}

// runTask is the per-task goroutine. It simulates a graph run with a
// short sleep (so cancel mid-stream is testable), streams task.running
// events, then emits the appropriate terminal event and clears busy.
//
// IMPORTANT: busy.Store(0) is called BEFORE writing the terminal event.
// This ensures that once the client reads task.complete or task.cancelled,
// the busy flag is already clear and a subsequent task.start on the same
// connection can proceed immediately (Bug 3 fix).
func runTask(
	log *slog.Logger,
	w *connWriter,
	busy *atomic.Int32,
	cancel context.CancelFunc,
	ctx context.Context,
	taskID string,
	prompt string,
) {
	defer cancel() // release the cancel func's resources

	log.Info("kenaz-harness-vm: task starting", "task_id", taskID)

	// Simulate streaming: emit a few task.running chunks with pauses
	// so that a cancel mid-stream is observable in tests.
	chunks := []string{"thinking... ", "working... ", "done."}
	for _, chunk := range chunks {
		select {
		case <-ctx.Done():
			// Cancelled mid-stream. Clear busy then emit task.cancelled (Bug 1 + 3 fix).
			log.Info("kenaz-harness-vm: task cancelled", "task_id", taskID)
			busy.Store(0)
			_ = w.send(msg{
				"kind":    "task.cancelled",
				"task_id": taskID,
			})
			return
		case <-time.After(20 * time.Millisecond):
			if err := w.send(msg{
				"kind":    "task.running",
				"task_id": taskID,
				"text":    chunk,
			}); err != nil {
				log.Warn("kenaz-harness-vm: write task.running failed", "err", err)
				busy.Store(0)
				return
			}
		}
	}

	// Final check: cancelled after last chunk but before complete.
	select {
	case <-ctx.Done():
		log.Info("kenaz-harness-vm: task cancelled after stream", "task_id", taskID)
		busy.Store(0)
		_ = w.send(msg{
			"kind":    "task.cancelled",
			"task_id": taskID,
		})
		return
	default:
	}

	log.Info("kenaz-harness-vm: task complete", "task_id", taskID)
	// Clear busy BEFORE writing task.complete so the client can immediately
	// send the next task.start without getting task_conflict (Bug 3 fix).
	busy.Store(0)
	_ = w.send(msg{
		"kind":    "task.complete",
		"task_id": taskID,
	})
}

// truncate returns s truncated to n runes with "..." suffix when truncation occurred.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}
