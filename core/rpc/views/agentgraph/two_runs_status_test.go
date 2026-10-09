package agentgraph_test

// dogfood 2026-10-08 round 2: "Run details" for a session's second chat
// turn showed the first turn's tokens and cost. Two chat runs in ONE
// session must each report their own llmTokens / llmCalls / costUsd.
//
// The defect turned out to be the view (RunView kept the first run's
// status when vue-router reused it for the second run id — pinned by
// frontend RunView.runSwitch.test.ts). This pins the backend half of the
// contract so a regression there cannot hide behind the frontend fix:
// statusFromLog is keyed by run id, not by session.

import (
	"context"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	graphview "github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph"
)

func appendChatRun(t *testing.T, log coreag.EventLog, runID, sessionID string, tokens int, cost float64) {
	t.Helper()
	var b coreag.EventBatch
	must := func(err error) {
		if err != nil {
			t.Fatalf("AppendKind: %v", err)
		}
	}
	must(b.AppendKind(runID, "", coreag.EventRunStart, map[string]any{"graph_id": "chat_default", "session_id": sessionID}))
	must(b.AppendKind(runID, "assistant_turn", coreag.EventLLMCall, map[string]any{"tokens": tokens, "cost_usd": cost, "model": "default"}))
	must(b.AppendKind(runID, "", coreag.EventRunComplete, map[string]any{"completed_nodes": 5}))
	for i := range b.Events {
		b.Events[i].SessionID = sessionID
	}
	if _, err := log.Append(b); err != nil {
		t.Fatalf("Append: %v", err)
	}
}

func TestGetRunStatus_TwoChatRunsInOneSessionReportTheirOwnCounters(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log := coreag.NewMemoryEventLog()
	const session = "b0c22dc553fb96ddeea681b7371f1ea2"
	appendChatRun(t, log, "chat-01M4F2AJBYA9FECJ90ENE4HT2K", session, 224802, 0.112409)
	appendChatRun(t, log, "chat-01M4F3ZVJ4Y1WQSMNJ526W84BG", session, 237262, 0.474604)

	mgr, err := graphview.NewManager(graphview.WithDataDir(t.TempDir()), graphview.WithEventLog(log))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	impl := graphview.New(mgr)

	second, err := impl.GetRunStatus(ctx, "chat-01M4F3ZVJ4Y1WQSMNJ526W84BG")
	if err != nil {
		t.Fatalf("GetRunStatus(second): %v", err)
	}
	if second.LLMTokens != 237262 || second.LLMCalls != 1 || second.CostUSD != 0.474604 {
		t.Fatalf("second run = tokens %d calls %d cost %v, want its own 237262 / 1 / 0.474604",
			second.LLMTokens, second.LLMCalls, second.CostUSD)
	}
	first, err := impl.GetRunStatus(ctx, "chat-01M4F2AJBYA9FECJ90ENE4HT2K")
	if err != nil {
		t.Fatalf("GetRunStatus(first): %v", err)
	}
	if first.LLMTokens != 224802 || first.CostUSD != 0.112409 {
		t.Fatalf("first run = tokens %d cost %v, want 224802 / 0.112409", first.LLMTokens, first.CostUSD)
	}
}
