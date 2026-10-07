package chat

// undelivered-message-retry (dogfood 2026-10-07): the terminal close
// says whether the model ACCEPTED the turn's request. A failure before
// any model output is "not delivered" (the user message never reached
// the model); a failure after streamed output keeps today's partial
// behaviour but now carries the classified reason too. The persisted
// half of this contract runs on real sqlite in
// core/rpc/chat_undelivered_retry_test.go.

import (
	"context"
	"strings"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

func TestDeliveryOutcome_ClosedPayload(t *testing.T) {
	cases := []struct {
		name          string
		resp          stubLLMResponse
		wantDelivered bool
		wantClass     corellm.FailureClass
		wantCode      string
	}{
		{
			name:          "rate limited before first token",
			resp:          stubLLMResponse{err: &corellm.ErrTransient{Status: 429, Message: "slow down"}},
			wantDelivered: false,
			wantClass:     corellm.FailureTransient,
			wantCode:      corellm.FailureCodeRateLimited,
		},
		{
			name:          "invalid key before first token",
			resp:          stubLLMResponse{err: &corellm.ErrAuth{Status: 401, Message: "bad key"}},
			wantDelivered: false,
			wantClass:     corellm.FailureUserActionable,
			wantCode:      corellm.FailureCodeAuthInvalid,
		},
		{
			name: "provider dropped mid-stream",
			resp: stubLLMResponse{
				stream: []coreag.StreamEvent{{Kind: coreag.StreamEventText, Text: "partial answ"}},
				err:    &corellm.ErrTransient{Status: 502, Message: "upstream reset"},
			},
			wantDelivered: true,
			wantClass:     corellm.FailureTransient,
			wantCode:      corellm.FailureCodeProviderDown,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			llm := &stubLLM{}
			llm.push(tc.resp)
			runner, broker, _ := buildIntegrationRunner(t, llm, newStubTools(), 25, []coreag.Message{
				{Role: "user", Content: "hi"},
			})
			turn := testTurn("hi")
			if _, err := runner.StartStream(context.Background(), "profile-1", "session-1", "", turn); err != nil {
				t.Fatalf("StartStream: %v", err)
			}
			closed := waitForClosed(t, broker)
			if closed.Reason != "backend-error" {
				t.Fatalf("reason = %q, want backend-error", closed.Reason)
			}
			if closed.Delivered != tc.wantDelivered {
				t.Errorf("delivered = %v, want %v", closed.Delivered, tc.wantDelivered)
			}
			if closed.FailureClass != string(tc.wantClass) || closed.FailureCode != tc.wantCode {
				t.Errorf("class/code = %q/%q, want %q/%q", closed.FailureClass, closed.FailureCode, tc.wantClass, tc.wantCode)
			}
			if closed.TurnSpanID != turn.MessageID {
				t.Errorf("turn_span_id = %q, want %q", closed.TurnSpanID, turn.MessageID)
			}
			if closed.FailureSummary == "" {
				t.Error("failure_summary empty")
			}
		})
	}
}

func TestDeliveryOutcome_CompletedIsDelivered(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{}
	llm.push(stubLLMResponse{
		stream: []coreag.StreamEvent{{Kind: coreag.StreamEventText, Text: "ok"}},
		resp:   coreag.LLMResponse{Content: "ok", FinishReason: "stop"},
	})
	runner, broker, _ := buildIntegrationRunner(t, llm, newStubTools(), 25, []coreag.Message{{Role: "user", Content: "hi"}})
	if _, err := runner.StartStream(context.Background(), "profile-1", "session-1", "", testTurn("hi")); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	closed := waitForClosed(t, broker)
	if closed.Reason != "completed" || !closed.Delivered || closed.FailureClass != "" {
		t.Fatalf("closed = %+v, want completed/delivered/no failure", closed)
	}
}

func TestStreamBridge_MoveStartIsNotDelivery(t *testing.T) {
	b := NewStreamBridge(&recordingBroker{}, "sub", "sess")
	b.Emit(coreag.StreamEvent{Kind: coreag.StreamEventMoveStart})
	if b.ModelResponded() {
		t.Fatal("a move boundary is announced before the provider is called; it is not delivery")
	}
	b.Emit(coreag.StreamEvent{Kind: coreag.StreamEventReasoning})
	if !b.ModelResponded() {
		t.Fatal("reasoning output means the model accepted the request")
	}
}

// TestDeliveryOutcome_CloseMessageRedacted: the close Message is
// Friendly() copy that embeds the provider body verbatim; a key echoed in
// that body must not reach the chat bubble.
func TestDeliveryOutcome_CloseMessageRedacted(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{}
	llm.push(stubLLMResponse{
		stream: []coreag.StreamEvent{{Kind: coreag.StreamEventText, Text: "partial"}},
		err: &corellm.ErrPaymentRequired{Status: 402,
			Message: "no credits for sk-or-v1-0123456789abcdef0123456789abcdef (Authorization: Bearer abcdefghijklmnop1234) see /v1?key=SECRETVALUE99"},
	})
	runner, broker, _ := buildIntegrationRunner(t, llm, newStubTools(), 25, []coreag.Message{{Role: "user", Content: "hi"}})
	if _, err := runner.StartStream(context.Background(), "profile-1", "session-1", "", testTurn("hi")); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	closed := waitForClosed(t, broker)
	for _, leak := range []string{"0123456789abcdef0123", "abcdefghijklmnop1234", "SECRETVALUE99"} {
		if strings.Contains(closed.Message, leak) || strings.Contains(closed.FailureMessage, leak) {
			t.Fatalf("close payload leaked %q: message=%q failure_message=%q", leak, closed.Message, closed.FailureMessage)
		}
	}
	if !closed.Delivered {
		t.Fatal("output streamed before the failure — delivered must be true")
	}
}
