package chat

// The journal's parked segment must be persisted BEFORE llm:stream-closed
// is observable: subscribers (the scheduled-chat dispatcher, the chat
// surface) read the transcript on that event. Flushing after the close
// let a reader see a turn with no assistant row and act on it (the
// scheduled-run cleanup deleted such a session, and the deferred flush
// then wrote into the deleted session).

import (
	"context"
	"sync"
	"testing"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
)

// closeSnapshotBroker records, at the instant stream-closed is emitted,
// what the history writer has persisted so far.
type closeSnapshotBroker struct {
	writer  *recordingHistoryWriter
	mu      sync.Mutex
	closed  bool
	atClose []writerCall
}

func (b *closeSnapshotBroker) Emit(_ string, payload any) {
	if _, ok := payload.(StreamClosedPayload); !ok {
		return
	}
	b.writer.mu.Lock()
	snap := append([]writerCall(nil), b.writer.calls...)
	b.writer.mu.Unlock()
	b.mu.Lock()
	b.closed, b.atClose = true, snap
	b.mu.Unlock()
}

func TestChatRunner_ParkedSegmentIsPersistedBeforeClose(t *testing.T) {
	t.Parallel()
	reg := &scriptedRegistry{}
	reg.push(textTurn("the parked answer"))
	writer := &recordingHistoryWriter{}
	broker := &closeSnapshotBroker{writer: writer}
	// A model node with no session_write after it: the fire's text is
	// parked by the journal and only the terminal flush writes it.
	graph := coreag.Graph{
		ID:          "test_flush_before_close",
		Entrypoints: []string{"llm1"},
		Nodes: []coreag.Node{
			{ID: "llm1", Kind: coreag.NodeKindModel, Attrs: coreag.ModelAttrs{
				Provider: "test", Model: "m", MaxTokens: 100, StreamToChat: true,
			}},
		},
	}
	runner, err := New(Config{
		Kernel:        coreag.NewKernel(),
		Registry:      reg,
		Broker:        broker,
		HistoryWriter: writer,
		History:       staticHistoryReader{},
		GraphLoader:   func() (coreag.Graph, error) { return graph, nil },
		MaxTurns:      func() int { return 25 },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	drainRunnerOnCleanup(t, runner)
	if _, err := runner.StartStream(context.Background(), "profile-1", "session-1", "", testTurn("hi")); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		broker.mu.Lock()
		closed, atClose := broker.closed, broker.atClose
		broker.mu.Unlock()
		if closed {
			for _, c := range atClose {
				if c.role == "assistant" && c.content == "the parked answer" {
					return
				}
			}
			t.Fatalf("at stream-closed the transcript had %+v — the parked segment was written after the close", atClose)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no stream-closed observed")
}
