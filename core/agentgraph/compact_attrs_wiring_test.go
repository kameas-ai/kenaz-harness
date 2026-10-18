package agentgraph_test

import (
	"reflect"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph"
)

// compact_attrs_wiring_test.go is G-2 (chat-turn-integrity-01PMZ606
// WP09): a table-driven gate over every CompactAttrs field, asserting
// each one either reaches a CompactionInput field (and therefore the
// compactor) or sits in a dated exemption with a named reason.
//
// THE POINT OF THIS GATE. CHAT-07 happened because CompactAttrs.Strategy
// existed, was authored by users, round-tripped through validation and
// codegen — and reached nothing but a log line. A field can look wired
// (it has a doc comment, a manifest entry, a Validate() rule) while
// still being decorative. This test does not re-prove the *behaviour*
// of the wired fields (compaction_target_test.go and
// compaction/pipeline_test.go do that, with real dispatch assertions);
// its job is narrower and more mechanical: nobody may add a ninth field
// to CompactAttrs, or move one between the two tables below, without
// this test forcing them to say where it goes. Allowlists shrink
// monotonically (CLAUDE.md) — the exemption table is the one part of
// this file that is allowed to grow, and only with a reason.
//
// wiredFields: CompactAttrs field name -> the CompactionInput field (or
// derived value) it reaches. Not required to share a name — e.g.
// TargetTokenBudget feeds CompactionInput.TargetTokens, and Provider +
// Model feed CompactionInput.ContextWindow indirectly via
// resolveContextWindow(env, a.Provider, a.Model).
var wiredFields = map[string]string{
	"Strategy":          "CompactionInput.Strategy (exec_compute.go compactExecutor.Execute) -> compaction.CompactRequest.Override",
	"SystemPrompt":      "CompactionInput.SystemPrompt (exec_compute.go compactExecutor.Execute)",
	"TargetTokenBudget": "CompactionInput.TargetTokens (target := a.TargetTokenBudget)",
	"Provider":          "CompactionInput.ContextWindow via resolveContextWindow(env, a.Provider, a.Model)",
	"Model":             "CompactionInput.ContextWindow via resolveContextWindow(env, a.Provider, a.Model)",
}

// exemptFields: CompactAttrs field name -> {reason, dated}. Every entry
// here is a field CompactionInput deliberately does not carry today.
var exemptFields = map[string]string{
	"MaxTokens": "2026-08 (compaction-convergence-01PMDL05 WP02 / " +
		"agentgraph-total-convergence-01PMGX01 WP08): this is the node's " +
		"*output* token cap, not a context-compaction budget — the two " +
		"were conflated once already (compaction_target_test.go pins the " +
		"separation) and MaxTokens must never reach CompactionInput.",
	"CustomSubgraphId": "2026-08 (chat-turn-integrity-01PMZ606 UNIT-5 E-003): " +
		"custom_subgraph has no production KernelRunner implementation " +
		"(docs/dead-code-audit-2026-08-18.md:1668,1778); justified at the " +
		"strategy-registration option site, not reachable from the compact " +
		"node until a KernelRunner exists.",
	"Temperature": "2026-08 (chat-turn-integrity-01PMZ606 WP09): compaction " +
		"strategies are deterministic transforms over the message slice " +
		"(drop_oldest, semantic_cluster) or drive their own LLM call " +
		"through CompactOpts.SummaryProvider/SummaryModel (summary) — " +
		"there is no sampling-temperature knob anywhere in the compaction " +
		"seam for this to reach.",
	"ToolAllowlist": "2026-08 (chat-turn-integrity-01PMZ606 WP09): the " +
		"compact node does not dispatch tool calls itself (custom_subgraph, " +
		"the one strategy that runs a sub-graph, has no production " +
		"KernelRunner — see CustomSubgraphId above), so there is no tool " +
		"dispatch for an allowlist to gate.",
}

// TestCompactAttrs_EveryFieldIsClassified is the gate. It fails closed:
// a new CompactAttrs field with no entry in either table fails here,
// naming the field, before it can ship as a silent CHAT-07 repeat.
func TestCompactAttrs_EveryFieldIsClassified(t *testing.T) {
	typ := reflect.TypeOf(agentgraph.CompactAttrs{})
	seen := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		seen[name] = true
		_, wired := wiredFields[name]
		reason, exempt := exemptFields[name]
		switch {
		case wired && exempt:
			t.Errorf("CompactAttrs.%s is listed as both wired and exempt — pick one", name)
		case !wired && !exempt:
			t.Errorf("CompactAttrs.%s has no entry in wiredFields or exemptFields — "+
				"classify it: either name the CompactionInput field it reaches, "+
				"or add a dated exemption reason (see CLAUDE.md's unwired-sweep G-2)", name)
		case exempt && reason == "":
			t.Errorf("CompactAttrs.%s exemption has an empty reason", name)
		}
	}
	// Catch the inverse drift too: an entry for a field that no longer
	// exists on the struct (renamed or removed) would otherwise sit
	// here forever looking like coverage.
	for name := range wiredFields {
		if !seen[name] {
			t.Errorf("wiredFields has entry %q but CompactAttrs has no such field — stale entry", name)
		}
	}
	for name := range exemptFields {
		if !seen[name] {
			t.Errorf("exemptFields has entry %q but CompactAttrs has no such field — stale entry", name)
		}
	}
}
