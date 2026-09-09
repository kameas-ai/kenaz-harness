package agentgraph

import (
	"context"
	"encoding/json"
	"testing"
)

// compaction_event_reporting_test.go closes the other half of CHAT-07
// (chat-turn-integrity-01PMZ606 WP09): once the compact node's Strategy
// attr actually reaches CompactRequest.Override (see
// core/agentgraph/compaction/pipeline_test.go
// TestPipeline_AgentgraphOverrideDispatchesAuthorsStrategy), the
// EventCompactionApplied payload must report what the compactor
// actually dispatched (CompactionOutput.Strategy), not the node's
// unresolved request attr. Reporting the attr is the exact defect this
// mission closes: an event describing a strategy that never ran.
//
// A stub Compactor is used here (not the real pipeline — that dispatch
// proof lives in the compaction package test above) specifically so the
// test can make CompactionOutput.Strategy diverge from the node's attr
// by construction, isolating the exec_compute.go event-emission line
// from the pipeline's own resolution logic.

// strategyMismatchCompactor always reports a *different* strategy than
// whatever CompactionInput.Strategy carried, so a test can tell whether
// the event source is co.Strategy (the dispatch) or a.Strategy (the
// request) purely from the reported value.
type strategyMismatchCompactor struct {
	calls    []CompactionInput
	dispatch string
}

func (s *strategyMismatchCompactor) Compact(_ context.Context, in CompactionInput) (CompactionOutput, error) {
	s.calls = append(s.calls, in)
	return CompactionOutput{
		Messages: []Message{{Role: "system", Content: "[compacted]"}},
		Strategy: s.dispatch,
	}, nil
}

// TestCompactNode_EventReportsDispatchedStrategyNotAttr drives a compact
// node whose `strategy` attr says "drop_oldest" against a Compactor that
// actually dispatches (and reports) "summary" — simulating exactly the
// CHAT-07 scenario where the resolved cascading config differs from the
// node's own request. The emitted event must say "summary" (what ran),
// never "drop_oldest" (what was asked for and did not run).
func TestCompactNode_EventReportsDispatchedStrategyNotAttr(t *testing.T) {
	src := `spec_version: "1"
id: compact_event_reporting
entrypoints: [c]
nodes:
  - id: c
    kind: compact
    attrs:
      strategy: drop_oldest
      target_token_budget: 100
`
	g, err := LoadYAML([]byte(src))
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}
	if err := Validate(g); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	log := NewMemoryEventLog()
	k := NewKernel(WithEventLog(log))
	comp := &strategyMismatchCompactor{dispatch: "summary"}
	env := &Env{
		RunID:     "run-compact-event-reporting",
		Graph:     &g,
		Compactor: comp,
	}
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Confirm the node's own attr really did reach the request, so a
	// failure below cannot be blamed on the request never carrying
	// "drop_oldest" in the first place.
	if len(comp.calls) != 1 {
		t.Fatalf("expected exactly 1 compactor invocation, got %d", len(comp.calls))
	}
	if comp.calls[0].Strategy != "drop_oldest" {
		t.Fatalf("compactor received Strategy = %q, want %q (the node's attr)", comp.calls[0].Strategy, "drop_oldest")
	}

	events := readEvents(t, log, env.RunID)
	var payload struct {
		Strategy string `json:"strategy"`
		Skipped  bool   `json:"skipped"`
	}
	found := false
	for _, e := range events {
		if e.Kind != EventCompactionApplied {
			continue
		}
		found = true
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			t.Fatalf("unmarshal EventCompactionApplied payload: %v", err)
		}
	}
	if !found {
		t.Fatalf("expected a compaction_applied event; got kinds: %v", kindsOf(events))
	}
	if payload.Strategy != "summary" {
		t.Fatalf("event strategy = %q, want %q (the strategy the compactor actually dispatched) — "+
			"got the node's unresolved attr instead, which is the CHAT-07 defect this test pins",
			payload.Strategy, "summary")
	}
}
