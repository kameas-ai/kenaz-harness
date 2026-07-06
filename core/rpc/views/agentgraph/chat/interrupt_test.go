package chat

import (
	"context"
	"strings"
	"sync"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
)

// TestInterruptState_markedText verifies the interrupt marker is appended
// to partial text (FR-001 — interrupt must be visible in history).
func TestInterruptState_markedText(t *testing.T) {
	tests := []struct {
		name        string
		partialText string
		wantSuffix  string
	}{
		{
			name:        "empty partial",
			partialText: "",
			wantSuffix:  interruptedByUserMarker,
		},
		{
			name:        "partial with text",
			partialText: "The answer is",
			wantSuffix:  interruptedByUserMarker,
		},
		{
			name:        "already marked",
			partialText: "hello " + interruptedByUserMarker,
			wantSuffix:  interruptedByUserMarker,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			is := &InterruptState{PartialText: tc.partialText}
			got := is.markedText()
			if !strings.HasSuffix(got, tc.wantSuffix) {
				t.Errorf("markedText() = %q, want suffix %q", got, tc.wantSuffix)
			}
		})
	}
}

// interruptHistoryWriter is a thin wrapper around recordingHistoryWriter
// for interrupt tests, reusing the type declared in chat_runner_test.go.
type interruptHistoryWriter struct {
	mu   sync.Mutex
	rows []interruptRow
}

type interruptRow struct {
	role    string
	content string
}

func (r *interruptHistoryWriter) AppendMessage(_ context.Context, _ string, role, content string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows = append(r.rows, interruptRow{role, content})
	return "intmsg-" + string(rune('a'+len(r.rows)-1)), nil
}

func (r *interruptHistoryWriter) snapshot() []interruptRow {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]interruptRow, len(r.rows))
	copy(out, r.rows)
	return out
}

// TestPersistInterrupt_APIValid verifies that after an interrupt the
// transcript has no unmatched tool_use: every dangling tool_use gets
// a synthetic tool result persisted (FR-001 acceptance criterion).
func TestPersistInterrupt_APIValid(t *testing.T) {
	writer := &interruptHistoryWriter{}

	is := &InterruptState{
		PartialText: "Calling the tools now",
		DanglingToolCalls: []coreag.ToolCallRequest{
			{ID: "tc1", Name: "bash", Arguments: `{"cmd":"ls"}`},
			{ID: "tc2", Name: "read_file", Arguments: `{"path":"/etc/hosts"}`},
		},
	}

	ctx := context.Background()
	mid := is.PersistInterrupt(ctx, "sess-001", writer)
	if mid == "" {
		t.Error("PersistInterrupt returned empty messageID for assistant row")
	}

	rows := writer.snapshot()
	// Expected: 1 assistant row + 2 tool-result rows = 3 total.
	if len(rows) != 3 {
		t.Fatalf("expected 3 appended rows, got %d: %+v", len(rows), rows)
	}

	// Row 0: assistant with marker.
	if rows[0].role != "assistant" {
		t.Errorf("rows[0].role = %q, want %q", rows[0].role, "assistant")
	}
	if !strings.Contains(rows[0].content, interruptedByUserMarker) {
		t.Errorf("assistant row does not contain interrupt marker: %q", rows[0].content)
	}

	// Rows 1-2: tool results with is_error indication.
	for i, row := range rows[1:] {
		if row.role != "tool" {
			t.Errorf("rows[%d].role = %q, want %q", i+1, row.role, "tool")
		}
		if !strings.Contains(row.content, "cancelled") {
			t.Errorf("rows[%d].content should mention cancelled: %q", i+1, row.content)
		}
	}
}

// TestPersistInterrupt_NilWriter verifies that a nil HistoryWriter
// does not panic (FR-001 defensive path).
func TestPersistInterrupt_NilWriter(t *testing.T) {
	is := &InterruptState{PartialText: "some text"}
	mid := is.PersistInterrupt(context.Background(), "sess-001", nil)
	if mid != "" {
		t.Errorf("expected empty messageID with nil writer, got %q", mid)
	}
}

// TestInterruptedByUserMarker_StopCalledPath verifies that the bridge
// records partial state before close — this exercises the bridge state
// that the WP01 interrupt path reads.
func TestInterruptedByUserMarker_StopCalledPath(t *testing.T) {
	// Build a broker that records closed payloads.
	var mu sync.Mutex
	var closedPayload StreamClosedPayload
	broker := &recordingBroker{}
	_ = broker // satisfy linter
	customBroker := brokerFuncChat(func(topic string, payload any) {
		if topic == "llm:stream-closed" {
			mu.Lock()
			if p, ok := payload.(StreamClosedPayload); ok {
				closedPayload = p
			}
			mu.Unlock()
		}
	})

	bridge := NewStreamBridge(customBroker, "sub-1", "sess-1")
	// Simulate some streamed text.
	bridge.Emit(coreag.StreamEvent{Kind: coreag.StreamEventText, Text: "Partial response"})

	// Verify partial state is accumulated before close.
	partialText, _ := bridge.PartialState()
	if partialText != "Partial response" {
		t.Errorf("bridge.PartialState() = %q, want %q", partialText, "Partial response")
	}

	// Simulate a stop-called close.
	bridge.EmitClosed("stop-called", "", "")

	mu.Lock()
	reason := closedPayload.Reason
	mu.Unlock()
	if reason != "stop-called" {
		t.Errorf("closed reason = %q, want %q", reason, "stop-called")
	}
}

// brokerFuncChat adapts a function to the Broker interface for interrupt tests.
// Named differently to avoid collision with other test helpers in the package.
type brokerFuncChat func(topic string, payload any)

func (f brokerFuncChat) Emit(topic string, payload any) { f(topic, payload) }
