package stdio

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// stubBroker is an in-memory EventPublisher that records every call.
type stubBroker struct {
	mu     sync.Mutex
	events []brokerCall
}

type brokerCall struct {
	topic   string
	payload any
}

func (b *stubBroker) Publish(topic string, payload any) {
	b.mu.Lock()
	b.events = append(b.events, brokerCall{topic: topic, payload: payload})
	b.mu.Unlock()
}

func (b *stubBroker) snapshot() []brokerCall {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]brokerCall, len(b.events))
	copy(out, b.events)
	return out
}

func TestProgressForwarder_Direct(t *testing.T) {
	t.Parallel()
	b := &stubBroker{}
	pf := NewProgressForwarder("brave-search", b, nil)

	pf.Register(42, "tok-A", "brave_web_search")

	total := 1.0
	raw, _ := json.Marshal(map[string]any{
		"progressToken": "tok-A",
		"progress":      0.5,
		"total":         total,
		"message":       "halfway",
	})
	pf.Handle(raw)

	events := b.snapshot()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	ev, ok := events[0].payload.(ProgressEvent)
	if !ok {
		t.Fatalf("payload type = %T", events[0].payload)
	}
	if events[0].topic != ProgressTopic {
		t.Fatalf("topic = %q", events[0].topic)
	}
	if ev.Server != "brave-search" || ev.RequestID != 42 || ev.Token != "tok-A" {
		t.Fatalf("event = %+v", ev)
	}
	if ev.Tool != "brave_web_search" {
		t.Fatalf("tool = %q", ev.Tool)
	}
	if ev.Progress != 0.5 || ev.Total == nil || *ev.Total != 1.0 {
		t.Fatalf("progress fields = %+v", ev)
	}
	if ev.Message != "halfway" {
		t.Fatalf("message = %q", ev.Message)
	}
}

func TestProgressForwarder_NumericToken(t *testing.T) {
	t.Parallel()
	b := &stubBroker{}
	pf := NewProgressForwarder("svc", b, nil)
	pf.Register(7, "9001", "tool-x") // numeric token stringified

	raw := json.RawMessage(`{"progressToken":9001,"progress":0.25}`)
	pf.Handle(raw)
	events := b.snapshot()
	if len(events) != 1 {
		t.Fatalf("events = %d", len(events))
	}
	ev := events[0].payload.(ProgressEvent)
	if ev.RequestID != 7 {
		t.Fatalf("numeric token failed to correlate; ev = %+v", ev)
	}
}

func TestProgressForwarder_UnknownToken(t *testing.T) {
	t.Parallel()
	b := &stubBroker{}
	pf := NewProgressForwarder("svc", b, nil)
	raw := json.RawMessage(`{"progressToken":"never-registered","progress":0.1}`)
	pf.Handle(raw)
	events := b.snapshot()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1 (best-effort emit)", len(events))
	}
	ev := events[0].payload.(ProgressEvent)
	if ev.RequestID != 0 {
		t.Fatalf("unknown token should publish RequestID=0; got %d", ev.RequestID)
	}
}

func TestProgressForwarder_NilPublisherNoop(t *testing.T) {
	t.Parallel()
	pf := NewProgressForwarder("svc", nil, nil)
	// Must not panic.
	pf.Register(1, "t", "x")
	pf.Handle(json.RawMessage(`{"progressToken":"t","progress":1}`))
	pf.Forget(1)
}

func TestProgressForwarder_MalformedDropped(t *testing.T) {
	t.Parallel()
	b := &stubBroker{}
	pf := NewProgressForwarder("svc", b, nil)
	pf.Handle(json.RawMessage(`{not-json`))
	pf.Handle(nil)
	if got := len(b.snapshot()); got != 0 {
		t.Fatalf("malformed payload published %d events", got)
	}
}

func TestProgressForwarder_Forget(t *testing.T) {
	t.Parallel()
	b := &stubBroker{}
	pf := NewProgressForwarder("svc", b, nil)
	pf.Register(1, "t", "x")
	pf.Forget(1)
	raw := json.RawMessage(`{"progressToken":"t","progress":1}`)
	pf.Handle(raw)
	ev := b.snapshot()[0].payload.(ProgressEvent)
	if ev.RequestID != 0 {
		t.Fatalf("Forget did not drop registration; ev = %+v", ev)
	}
}

// TestProgress_EndToEnd_ServerNotification drives the fake server with
// --progress-on-call; it emits a notifications/progress mid-flight.
// Asserts the broker received a ProgressEvent for the in-flight
// request id.
func TestProgress_EndToEnd_ServerNotification(t *testing.T) {
	t.Parallel()
	bin := buildFakeServer(t)

	b := &stubBroker{}
	inst := newServerInstance("fake", nil, nil, nil, b, instanceOptions{})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := inst.Spawn(ctx, SpawnSpec{
		ID:      "fake",
		Command: []string{bin, "--progress-on-call"},
	}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer inst.Close(context.Background())

	if _, err := inst.CallToolWithProgress(ctx, "fake_echo", json.RawMessage(`{"x":1}`), "tok-mid"); err != nil {
		t.Fatalf("CallToolWithProgress: %v", err)
	}

	// The server emits the progress notification before the tools/call
	// response, but reader-loop ordering is async. Wait briefly.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, ev := range b.snapshot() {
			if ev.topic != ProgressTopic {
				continue
			}
			pe, ok := ev.payload.(ProgressEvent)
			if !ok {
				continue
			}
			if pe.Server == "fake" && pe.Token == "tok-mid" && pe.Tool == "fake_echo" {
				if pe.Progress != 0.5 {
					t.Fatalf("progress = %v", pe.Progress)
				}
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("progress event never reached broker")
}
