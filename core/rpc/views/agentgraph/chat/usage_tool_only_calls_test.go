package chat

// dogfood 2026-10-08 round 2: the session footer showed 2,078,630 tok /
// $1.0439 where the provider had billed 3,000,619 tok / $1.5059 — turns
// with a 3-call tool loop contributed one call each. The calls that went
// missing were the ones whose only output was tool calls: they produce
// no transcript row, and usage.Add bills by UPDATEing a row, so their
// usage had nowhere to land.
//
// Real kernel + real LLMProviderAdapter + real turnJournal + REAL sqlite
// (buildMoveRunnerRealSQLite; CLAUDE.md blind spot #2).

import (
	"context"
	"testing"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// toolOnlyTurnWithUsage scripts a fire that streams NO text and only
// requests a tool — the shape that used to drop its usage.
func toolOnlyTurnWithUsage(callID, toolName, argsJSON string, promptTok, complTok int, cost float64) scriptedTurn {
	return scriptedTurn{
		resp: corellm.Response{
			FinishReason: "tool_use",
			ToolCalls:    []corellm.ToolUse{{ID: callID, Name: toolName, Input: []byte(argsJSON)}},
			Usage:        corellm.Usage{InputTokens: promptTok, OutputTokens: complTok},
			Cost:         corellm.Cost{Currency: "USD", Total: cost, Source: "provider"},
		},
	}
}

func TestUsage_ToolOnlyCallsAreCountedInTheCumulativeTotal(t *testing.T) {
	ctx := context.Background()
	wants := []usageWant{
		{prompt: 224798, completion: 31, cost: 0.1124},
		{prompt: 225410, completion: 44, cost: 0.1129},
		{prompt: 226002, completion: 120, cost: 0.1136}, // final answer
	}
	reg := &scriptedRegistry{}
	reg.push(toolOnlyTurnWithUsage("tu-1", "search__web", `{"q":"a"}`, wants[0].prompt, wants[0].completion, wants[0].cost))
	reg.push(toolOnlyTurnWithUsage("tu-2", "search__web", `{"q":"b"}`, wants[1].prompt, wants[1].completion, wants[1].cost))
	reg.push(textTurnWithUsage("found it", wants[2].prompt, wants[2].completion, wants[2].cost))
	pool := &scriptedPool{entries: []ToolEntry{{Server: "search", Name: "web"}}}

	runner, broker, sessionMgr, usageMgr, _ := buildMoveRunnerRealSQLite(t, reg, pool, loadProductionChatGraph(t))
	rec, err := sessionMgr.Create(ctx, "tool-only usage")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := runner.StartStream(ctx, "profile-1", rec.ID, "", testTurn("look it up")); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	if closed := waitForClosed(t, broker); closed.Reason == "backend-error" {
		t.Fatalf("run failed: %s", closed.Message)
	}

	var wantPrompt, wantCompletion int
	var wantCost float64
	for _, w := range wants {
		wantPrompt += w.prompt
		wantCompletion += w.completion
		wantCost += w.cost
	}
	agg, err := usageMgr.GetSession(ctx, rec.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if agg.PromptTokens != wantPrompt || agg.CompletionTokens != wantCompletion {
		t.Errorf("aggregate tokens = %d/%d, want %d/%d — the SUM of all three calls, tool-only ones included",
			agg.PromptTokens, agg.CompletionTokens, wantPrompt, wantCompletion)
	}
	if !approxEqualUSD(agg.CostUSD, wantCost, 1e-9) {
		t.Errorf("aggregate cost = %v, want %v (sum of all three calls)", agg.CostUSD, wantCost)
	}

	// last_usage stays the LAST call: it drives the context-window bar,
	// and a sum would read as a context 3x the real one.
	last, err := sessionMgr.GetLastUsage(ctx, rec.ID)
	if err != nil {
		t.Fatalf("GetLastUsage: %v", err)
	}
	final := wants[2]
	if last.PromptTokens != final.prompt || last.CompletionTokens != final.completion || !approxEqualUSD(last.CostUSD, final.cost, 1e-9) {
		t.Errorf("last_usage = %d/%d/$%v, want the third call's %d/%d/$%v",
			last.PromptTokens, last.CompletionTokens, last.CostUSD, final.prompt, final.completion, final.cost)
	}
}
