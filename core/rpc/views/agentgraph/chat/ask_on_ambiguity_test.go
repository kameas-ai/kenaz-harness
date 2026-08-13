package chat

// autonomy-knobs-live-01PMAG02 WP02 — askOnAmbiguity.
//
// Two consumers (spec §3.1):
//
//  1. Catalog shaping — withholdsAskTool / filterOutTool decide whether
//     kenaz__ask_user_question is offered to the model at all, and
//     buildAskBarBlock states the bar for using it at hard/major.
//  2. askExecutor's DefaultAnswer fallback (pinned in
//     core/agentgraph/exec_compute_test.go) — driven by
//     applyAskOnAmbiguityDial here.

import (
	"strings"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/autonomy"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/tools/askuserquestion"
)

// withholdsAskTool: only proceed/never withhold the tool.
func TestWithholdsAskTool(t *testing.T) {
	t.Parallel()
	cases := []struct {
		mode autonomy.AskMode
		want bool
	}{
		{autonomy.AskAlways, false},
		{autonomy.AskHard, false},
		{autonomy.AskMajor, false},
		{autonomy.AskProceed, true},
		{autonomy.AskNever, true},
		{autonomy.AskMode(""), false}, // zero value: nil AutonomyKnobs provider
	}
	for _, tc := range cases {
		if got := withholdsAskTool(tc.mode); got != tc.want {
			t.Errorf("withholdsAskTool(%q) = %v, want %v", tc.mode, got, tc.want)
		}
	}
}

func TestFilterOutTool(t *testing.T) {
	t.Parallel()
	in := []corellm.ToolSpec{
		{Name: "kenaz__read_file"},
		{Name: askuserquestion.ToolName},
		{Name: "kenaz__write_file"},
	}
	got := filterOutTool(in, askuserquestion.ToolName)
	if len(got) != 2 {
		t.Fatalf("filtered catalog has %d tools, want 2: %+v", len(got), got)
	}
	for _, tl := range got {
		if tl.Name == askuserquestion.ToolName {
			t.Fatalf("ask tool survived the filter: %+v", got)
		}
	}
	// A miss returns the input unchanged (no spurious allocation/mutation).
	noMatch := filterOutTool(in, "not_present")
	if len(noMatch) != len(in) {
		t.Errorf("no-match filter changed length: got %d, want %d", len(noMatch), len(in))
	}
}

// FR-005: an empty/nil catalog and a name with no match must not panic
// and must not fabricate entries.
func TestFilterOutTool_EmptyInput(t *testing.T) {
	t.Parallel()
	if got := filterOutTool(nil, askuserquestion.ToolName); len(got) != 0 {
		t.Errorf("nil input produced %+v, want empty", got)
	}
}

// applyAskOnAmbiguityDial: only AskNever populates DefaultAnswer.
func TestApplyAskOnAmbiguityDial_NeverSetsDefaultAnswer(t *testing.T) {
	t.Parallel()
	g := coreag.Graph{Nodes: []coreag.Node{
		{ID: "ask_user", Kind: coreag.NodeKindAsk, Attrs: coreag.AskAttrs{Question: "what?"}},
	}}
	applyAskOnAmbiguityDial(&g, autonomy.AskNever)
	got := g.Nodes[0].Attrs.(coreag.AskAttrs).DefaultAnswer
	if got == "" {
		t.Fatal("AskNever left DefaultAnswer empty — an unseeded AskNode would still pause")
	}
}

// FR-005: every other mode (including the zero value) leaves AskAttrs
// byte-identical to the pre-mission shape.
func TestApplyAskOnAmbiguityDial_OtherModesLeaveAttrsUntouched(t *testing.T) {
	t.Parallel()
	for _, mode := range []autonomy.AskMode{
		autonomy.AskAlways, autonomy.AskHard, autonomy.AskMajor, autonomy.AskProceed, autonomy.AskMode(""),
	} {
		g := coreag.Graph{Nodes: []coreag.Node{
			{ID: "ask_user", Kind: coreag.NodeKindAsk, Attrs: coreag.AskAttrs{Question: "what?"}},
		}}
		applyAskOnAmbiguityDial(&g, mode)
		got := g.Nodes[0].Attrs.(coreag.AskAttrs)
		if got.DefaultAnswer != "" {
			t.Errorf("mode %q set DefaultAnswer = %q, want empty (FR-005)", mode, got.DefaultAnswer)
		}
	}
}

// applyAskOnAmbiguityDial must not clobber a DefaultAnswer a graph
// author already declared explicitly.
func TestApplyAskOnAmbiguityDial_DoesNotClobberExistingDefault(t *testing.T) {
	t.Parallel()
	g := coreag.Graph{Nodes: []coreag.Node{
		{ID: "ask_user", Kind: coreag.NodeKindAsk, Attrs: coreag.AskAttrs{
			Question: "what?", DefaultAnswer: "author-declared default",
		}},
	}}
	applyAskOnAmbiguityDial(&g, autonomy.AskNever)
	got := g.Nodes[0].Attrs.(coreag.AskAttrs).DefaultAnswer
	if got != "author-declared default" {
		t.Errorf("DefaultAnswer = %q, want the author's own value untouched", got)
	}
}

// applyAskOnAmbiguityDial ignores non-Ask nodes.
func TestApplyAskOnAmbiguityDial_IgnoresNonAskNodes(t *testing.T) {
	t.Parallel()
	g := coreag.Graph{Nodes: []coreag.Node{
		{ID: "loop", Kind: coreag.NodeKindLoop, Attrs: coreag.LoopAttrs{MaxIterations: 5}},
	}}
	applyAskOnAmbiguityDial(&g, autonomy.AskNever)
	got := g.Nodes[0].Attrs.(coreag.LoopAttrs)
	if got.MaxIterations != 5 {
		t.Errorf("non-Ask node was mutated: %+v", got)
	}
}

// buildAskBarBlock: hard/major produce distinct, non-empty guidance;
// every other mode (including proceed/never, where the tool is absent
// from the catalog entirely — a bar for an unreachable tool is moot)
// and a nil resolver append nothing.
func TestBuildAskBarBlock(t *testing.T) {
	t.Parallel()
	seen := map[string]autonomy.AskMode{}
	for _, mode := range []autonomy.AskMode{autonomy.AskHard, autonomy.AskMajor} {
		a := &LLMProviderAdapter{askOnAmbiguity: func() autonomy.AskMode { return mode }}
		got := a.buildAskBarBlock()
		if strings.TrimSpace(got) == "" {
			t.Errorf("mode %q produced an empty bar", mode)
			continue
		}
		if prev, dup := seen[got]; dup {
			t.Errorf("mode %q produced the same bar text as %q", mode, prev)
		}
		seen[got] = mode
	}
	if len(seen) != 2 {
		t.Fatalf("distinct bars = %d, want 2", len(seen))
	}

	for _, mode := range []autonomy.AskMode{autonomy.AskAlways, autonomy.AskProceed, autonomy.AskNever, autonomy.AskMode("")} {
		a := &LLMProviderAdapter{askOnAmbiguity: func() autonomy.AskMode { return mode }}
		if got := a.buildAskBarBlock(); got != "" {
			t.Errorf("mode %q produced %q, want empty", mode, got)
		}
	}

	if got := (&LLMProviderAdapter{}).buildAskBarBlock(); got != "" {
		t.Fatalf("nil resolver produced %q, want empty", got)
	}
	var nilAdapter *LLMProviderAdapter
	if got := nilAdapter.buildAskBarBlock(); got != "" {
		t.Fatalf("nil adapter produced %q, want empty", got)
	}
}
