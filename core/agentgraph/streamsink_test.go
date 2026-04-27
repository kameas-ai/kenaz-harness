package agentgraph

import (
	"context"
	"sync"
	"testing"
)

// recordingStreamSink is a goroutine-safe StreamSink that captures
// every Emit/Close call. Used to assert the kernel + LLMNode propagate
// the sink onto the chassis-bound provider's ctx without changing the
// LLMProvider seam.
type recordingStreamSink struct {
	mu      sync.Mutex
	events  []StreamEvent
	closed  int
}

func (r *recordingStreamSink) Emit(ev StreamEvent) {
	r.mu.Lock()
	r.events = append(r.events, ev)
	r.mu.Unlock()
}

func (r *recordingStreamSink) Close() {
	r.mu.Lock()
	r.closed++
	r.mu.Unlock()
}

func (r *recordingStreamSink) snapshot() ([]StreamEvent, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := append([]StreamEvent(nil), r.events...)
	return out, r.closed
}

// TestStreamSink_PropagatesViaContext verifies that the modelExecutor
// pins env.StreamSink onto the LLMProvider call's ctx so a chassis
// adapter can pump streaming chunks without a new seam argument.
func TestStreamSink_PropagatesViaContext(t *testing.T) {
	sink := &recordingStreamSink{}

	// LLMProvider that pulls the sink off ctx and emits a couple of
	// fake deltas before returning the terminal response — exactly the
	// shape the chassis adapter will use.
	provider := LLMProviderFunc(func(ctx context.Context, _ LLMRequest) (LLMResponse, error) {
		s, ok := StreamSinkFromContext(ctx)
		if !ok {
			t.Errorf("expected StreamSink on ctx; got none")
			return LLMResponse{Content: "ok", FinishReason: "stop"}, nil
		}
		s.Emit(StreamEvent{Kind: StreamEventText, Text: "hello "})
		s.Emit(StreamEvent{Kind: StreamEventText, Text: "world"})
		s.Emit(StreamEvent{Kind: StreamEventFinish, Finish: "stop"})
		s.Close()
		return LLMResponse{Content: "hello world", FinishReason: "stop"}, nil
	})

	env := &Env{
		LLM:        provider,
		StreamSink: sink,
		Counters:   &RunCounters{},
		State:      NewRunState(),
	}
	applyEnvDefaults(env)

	node := &Node{
		ID:    "llm",
		Kind:  NodeKindModel,
		Attrs: ModelAttrs{Provider: "test", Model: "test", MaxTokens: 100},
	}
	exec := modelExecutor{}
	if _, err := exec.Execute(context.Background(), env, node, PortValues{}); err != nil {
		t.Fatalf("Execute: unexpected err: %v", err)
	}

	events, closes := sink.snapshot()
	if len(events) != 3 {
		t.Fatalf("expected 3 stream events; got %d (%v)", len(events), events)
	}
	if events[0].Text != "hello " || events[1].Text != "world" {
		t.Errorf("text deltas out of order: %+v", events)
	}
	if events[2].Kind != StreamEventFinish || events[2].Finish != "stop" {
		t.Errorf("expected finish event last; got %+v", events[2])
	}
	if closes != 1 {
		t.Errorf("expected exactly 1 Close; got %d", closes)
	}
}

// TestStreamSink_NilSafe verifies that an LLMNode with a nil StreamSink
// still completes (the chassis-bound provider just doesn't pump
// deltas). Production wiring currently leaves the sink nil for kernel
// tests, scripted activities, and batch executions.
func TestStreamSink_NilSafe(t *testing.T) {
	provider := LLMProviderFunc(func(ctx context.Context, _ LLMRequest) (LLMResponse, error) {
		if _, ok := StreamSinkFromContext(ctx); ok {
			t.Errorf("expected no StreamSink on ctx; got one")
		}
		return LLMResponse{Content: "ok", FinishReason: "stop"}, nil
	})

	env := &Env{
		LLM:      provider,
		Counters: &RunCounters{},
		State:    NewRunState(),
	}
	applyEnvDefaults(env)

	node := &Node{
		ID:    "llm",
		Kind:  NodeKindModel,
		Attrs: ModelAttrs{Provider: "test", Model: "test", MaxTokens: 100},
	}
	exec := modelExecutor{}
	if _, err := exec.Execute(context.Background(), env, node, PortValues{}); err != nil {
		t.Fatalf("Execute: unexpected err: %v", err)
	}
}

// TestStreamSink_NewStreamSinkFunc verifies the function-adapter helper
// dispatches Emit/Close to the underlying callbacks and treats nil
// callbacks as no-ops.
func TestStreamSink_NewStreamSinkFunc(t *testing.T) {
	var emitted []StreamEvent
	var closed bool
	sink := NewStreamSinkFunc(
		func(ev StreamEvent) { emitted = append(emitted, ev) },
		func() { closed = true },
	)
	sink.Emit(StreamEvent{Kind: StreamEventText, Text: "x"})
	sink.Close()
	if len(emitted) != 1 || emitted[0].Text != "x" {
		t.Errorf("emit not forwarded: %+v", emitted)
	}
	if !closed {
		t.Errorf("close not forwarded")
	}

	// nil callbacks must not panic.
	noop := NewStreamSinkFunc(nil, nil)
	noop.Emit(StreamEvent{Kind: StreamEventFinish})
	noop.Close()
}
