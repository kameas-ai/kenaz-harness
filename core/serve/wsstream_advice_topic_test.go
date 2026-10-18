package serve_test

// wsstream_advice_topic_test.go — laya-advisors-01LAYA001 WP07's served-
// mode delivery proof for the advisor seam's two topics
// (chat.TopicAdviceRecommendation, chat.TopicAdviceAutoActed). Mirrors
// wsstream_gap_topics_test.go's pattern exactly: a real core.New + rpc.New
// chassis, a real served.Server, a real WebSocket connection, and
// api.EventBus().Publish (the exact call fireAdvice/autoActBranchNow make
// downstream of their own broker.Emit) — asserting the frame actually
// arrives, and that a DIFFERENT session's connection does not receive it
// (D-705's fail-closed session filter).
//
// The brief's own instruction: "verify DELIVERED under served mode, not
// just allowlisting." Both topics are session-scoped (AdviceChipPayload /
// AdviceAutoActedPayload both carry session_id), so this is the same
// disposition class as elicit:deferred, not a processWideTopics entry.

import (
	"encoding/json"
	"testing"
	"time"

	chatview "github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph/chat"
)

func TestServedAdviceRecommendation_ScopedToSubscribedSession(t *testing.T) {
	api, baseURL, cancel := newChatHarness(t)
	defer cancel()

	wsA := dialChatWS(t, baseURL, "sess-A")
	defer wsA.Close() //nolint:errcheck
	wsB := dialChatWS(t, baseURL, "sess-B")
	defer wsB.Close() //nolint:errcheck

	api.EventBus().Publish(chatview.TopicAdviceRecommendation, chatview.AdviceChipPayload{
		SessionID:     "sess-A",
		KindID:        "branch_now",
		Decision:      true,
		Confidence:    88,
		Model:         "heuristic/branch-regex-v1",
		Rung:          "heuristic",
		Title:         "Branch this off?",
		Body:          "This looks like a separable thread.",
		PromptVersion: "v1",
	})

	frame := readUntil(t, wsA, chatview.TopicAdviceRecommendation, 2*time.Second)
	var payload chatview.AdviceChipPayload
	if err := json.Unmarshal(frame.Data, &payload); err != nil {
		t.Fatalf("unmarshal %s frame data: %v (raw: %s)", chatview.TopicAdviceRecommendation, err, frame.Data)
	}
	if payload.KindID != "branch_now" {
		t.Errorf("payload.KindID = %q, want %q", payload.KindID, "branch_now")
	}
	if payload.Confidence != 88 {
		t.Errorf("payload.Confidence = %d, want 88", payload.Confidence)
	}

	assertNoFrame(t, wsB, 300*time.Millisecond, "sess-B connection, advice:recommendation for sess-A")
}

func TestServedAdviceAutoActed_ScopedToSubscribedSession(t *testing.T) {
	api, baseURL, cancel := newChatHarness(t)
	defer cancel()

	wsA := dialChatWS(t, baseURL, "sess-A")
	defer wsA.Close() //nolint:errcheck
	wsB := dialChatWS(t, baseURL, "sess-B")
	defer wsB.Close() //nolint:errcheck

	api.EventBus().Publish(chatview.TopicAdviceAutoActed, chatview.AdviceAutoActedPayload{
		SessionID:      "sess-A",
		KindID:         "branch_now",
		ChildSessionID: "sess-A-child-1",
		Confidence:     95,
		Model:          "heuristic/branch-regex-v1",
	})

	frame := readUntil(t, wsA, chatview.TopicAdviceAutoActed, 2*time.Second)
	var payload chatview.AdviceAutoActedPayload
	if err := json.Unmarshal(frame.Data, &payload); err != nil {
		t.Fatalf("unmarshal %s frame data: %v (raw: %s)", chatview.TopicAdviceAutoActed, err, frame.Data)
	}
	if payload.ChildSessionID != "sess-A-child-1" {
		t.Errorf("payload.ChildSessionID = %q, want %q", payload.ChildSessionID, "sess-A-child-1")
	}

	assertNoFrame(t, wsB, 300*time.Millisecond, "sess-B connection, advice:auto-acted for sess-A")
}
