package rpc

import (
	"context"
	"sync"
	"testing"
	"time"
)

// recordingEmitter captures every Emit call for assertion.
type recordingEmitter struct {
	mu    sync.Mutex
	calls []emitCall
}

type emitCall struct {
	topic   string
	payload any
}

func (r *recordingEmitter) Emit(_ context.Context, topic string, payload any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, emitCall{topic: topic, payload: payload})
}

func (r *recordingEmitter) topics() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.calls))
	for i, c := range r.calls {
		out[i] = c.topic
	}
	return out
}

func (r *recordingEmitter) closeForSubscription(id string) (StreamClosedPayload, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.calls {
		if p, ok := c.payload.(StreamClosedPayload); ok && p.ID == id {
			return p, true
		}
	}
	return StreamClosedPayload{}, false
}

func TestStreamBrokerEmitsAndCloses(t *testing.T) {
	rec := &recordingEmitter{}
	broker := NewStreamBroker(rec)

	src := make(chan any, 4)
	src <- "evt-1"
	src <- "evt-2"

	id, err := broker.Subscribe(context.Background(), "sessions", "event", src)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if id == "" {
		t.Fatalf("Subscribe returned empty id")
	}

	// Allow pump to drain.
	time.Sleep(50 * time.Millisecond)

	close(src)
	time.Sleep(50 * time.Millisecond)

	topics := rec.topics()
	wantPrefix := "sessions:event"
	count := 0
	for _, t0 := range topics {
		if t0 == wantPrefix {
			count++
		}
	}
	if count != 2 {
		t.Errorf("expected 2 sessions:event emissions, got %d (topics=%v)", count, topics)
	}

	payload, ok := rec.closeForSubscription(id)
	if !ok {
		t.Fatalf("missing stream-closed payload for %q (topics=%v)", id, topics)
	}
	if payload.Reason != StreamClosedBackendError {
		t.Errorf("close reason = %q, want %q", payload.Reason, StreamClosedBackendError)
	}
}

func TestStreamBrokerUnsubscribe(t *testing.T) {
	rec := &recordingEmitter{}
	broker := NewStreamBroker(rec)

	src := make(chan any, 1)
	id, err := broker.Subscribe(context.Background(), "mcp", "event", src)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if err := broker.Unsubscribe(id); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	payload, ok := rec.closeForSubscription(id)
	if !ok {
		t.Fatalf("missing stream-closed payload for %q", id)
	}
	if payload.Reason != StreamClosedStopCalled {
		t.Errorf("close reason = %q, want %q", payload.Reason, StreamClosedStopCalled)
	}
}

func TestStreamBrokerCtxCancel(t *testing.T) {
	rec := &recordingEmitter{}
	broker := NewStreamBroker(rec)

	ctx, cancel := context.WithCancel(context.Background())
	src := make(chan any, 1)
	id, err := broker.Subscribe(ctx, "audit", "event", src)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	cancel()
	time.Sleep(50 * time.Millisecond)

	payload, ok := rec.closeForSubscription(id)
	if !ok {
		t.Fatalf("missing stream-closed payload for %q", id)
	}
	if payload.Reason != StreamClosedCtxCancelled {
		t.Errorf("close reason = %q, want %q", payload.Reason, StreamClosedCtxCancelled)
	}
}
