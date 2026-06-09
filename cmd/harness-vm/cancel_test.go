package main

import (
	"testing"
	"time"
)

// TestCancelDropsMidStream verifies that sending task.cancel while a task is
// running results in a task.cancelled event being delivered to the client.
//
// Failure mode before fix (Bug 1): server cancelled the context but never
// wrote task.cancelled back to the connection.
func TestCancelDropsMidStream(t *testing.T) {
	srv, addr := startTestServer(t, "")
	defer srv.Close()

	conn := dialAndAuth(t, addr, "")
	defer conn.Close()

	msgs, mu, stop := readMessages(t, conn)
	defer stop()

	// Start a long-running task.
	sendMsg(t, conn, map[string]any{
		"kind":    "task.start",
		"task_id": "task-cancel-1",
		"prompt":  "do something slow",
	})

	// Wait briefly then cancel mid-stream.
	time.Sleep(30 * time.Millisecond)
	sendMsg(t, conn, map[string]any{
		"kind":    "task.cancel",
		"task_id": "task-cancel-1",
	})

	// Expect task.cancelled within reasonable time.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if time.Now().After(deadline) {
			mu.Lock()
			got := make([]map[string]any, len(*msgs))
			copy(got, *msgs)
			mu.Unlock()
			t.Fatalf("expected task.cancelled; got %v", got)
		}
		mu.Lock()
		found := findKind(*msgs, "task.cancelled")
		mu.Unlock()
		if found != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
}
