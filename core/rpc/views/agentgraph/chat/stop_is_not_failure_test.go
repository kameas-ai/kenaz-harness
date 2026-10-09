package chat

// dogfood 2026-10-08 round 2: clicking Cancel mid-generation showed the
// composer error bar "The model request failed. loop: node "agent_loop":
// … chat: stream final: llm: cancelled: context" and the bubble later
// read "Connection lost — partial reply preserved. Resume". Provider
// adapters report a cancelled stream as *llm.ErrCancelled, which did not
// match context.Canceled, so the runner's Stop arm never fired and the
// Stop was classified as a backend failure.

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// streamingUntilCancelledLLM streams some text, then blocks until its
// ctx is cancelled and reports the cancellation the way the OpenRouter /
// openaiwire adapters do: &llm.ErrCancelled{Reason: "context"}, NOT the
// ctx error.
type streamingUntilCancelledLLM struct {
	once      sync.Once
	streaming chan struct{}
}

func (f *streamingUntilCancelledLLM) Generate(ctx context.Context, _ coreag.LLMRequest) (coreag.LLMResponse, error) {
	if sink, ok := coreag.StreamSinkFromContext(ctx); ok && sink != nil {
		sink.Emit(coreag.StreamEvent{Kind: coreag.StreamEventText, Text: "Here is the first part of a long answer"})
	}
	f.once.Do(func() { close(f.streaming) })
	<-ctx.Done()
	return coreag.LLMResponse{}, fmt.Errorf("chat: stream final: %w", &corellm.ErrCancelled{Reason: "context"})
}

func TestChatRunner_StopMidStream_IsStopNotFailure(t *testing.T) {
	t.Parallel()
	llm := &streamingUntilCancelledLLM{streaming: make(chan struct{})}
	runner, broker := buildOverflowStopRunner(t, llm, 1)

	subID, err := runner.StartStream(context.Background(), "profile-1", "session-1", "model-1", testTurn("write 500 words"))
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	select {
	case <-llm.streaming:
	case <-time.After(2 * time.Second):
		t.Fatal("model never started streaming")
	}
	if err := runner.StopStream(context.Background(), subID); err != nil {
		t.Fatalf("StopStream: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range broker.snapshot() {
			closed, ok := e.payload.(StreamClosedPayload)
			if !ok {
				continue
			}
			if closed.Reason != "stop-called" {
				t.Fatalf("stream-closed reason = %q (message %q), want stop-called — a user Stop is not a backend error",
					closed.Reason, closed.Message)
			}
			if closed.FailureCode != "" || closed.FailureSummary != "" {
				t.Errorf("a Stop carries a failure (%q / %q)", closed.FailureCode, closed.FailureSummary)
			}
			if closed.PartialMessageID != "" {
				t.Errorf("a Stop persisted a resumable partial %q — that is the connection-drop path", closed.PartialMessageID)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no stream-closed payload after Stop; events = %+v", broker.snapshot())
}
