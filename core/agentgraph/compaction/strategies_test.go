package compaction_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/agentgraph/compaction"
)

// fakeLLM is a deterministic LLMSummarizer used by the summary tests.
type fakeLLM struct {
	resp string
	err  error
	seen agentgraph.LLMRequest
}

func (f *fakeLLM) Generate(_ context.Context, req agentgraph.LLMRequest) (agentgraph.LLMResponse, error) {
	f.seen = req
	if f.err != nil {
		return agentgraph.LLMResponse{}, f.err
	}
	return agentgraph.LLMResponse{Content: f.resp}, nil
}

// fakeEmbedder returns deterministic vectors keyed by string hash.
type fakeEmbedder struct {
	dims int
	err  error
}

func (e *fakeEmbedder) Dimensions() int { return e.dims }
func (e *fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	if e.err != nil {
		return nil, e.err
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, e.dims)
		// Hash the text into the vector slot — same content yields the
		// same vector so we can assert clustering shape.
		seed := int64(0)
		for _, b := range []byte(t) {
			seed = seed*31 + int64(b)
		}
		for j := 0; j < e.dims; j++ {
			seed = seed*1103515245 + 12345
			v[j] = float32((seed>>16)&0xFFFF) / 65535.0
		}
		out[i] = v
	}
	return out, nil
}

// ---- DropOldestStrategy ----

func TestDropOldestStrategy_DropsUntilUnderTarget(t *testing.T) {
	s := compaction.NewDropOldestStrategy()
	msgs := []agentgraph.Message{
		{Role: "user", Content: strings.Repeat("a", 80)},
		{Role: "assistant", Content: strings.Repeat("b", 80)},
		{Role: "user", Content: strings.Repeat("c", 80)},
		{Role: "assistant", Content: strings.Repeat("d", 80)},
	}
	res, err := s.Compact(context.Background(), compaction.ContextSlice{
		Messages:     msgs,
		TargetTokens: 30, // ≈ 120 bytes
	}, compaction.CompactOpts{})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if res.Strategy != compaction.StrategyDropOldest {
		t.Fatalf("strategy = %q", res.Strategy)
	}
	if len(res.Messages) >= len(msgs) {
		t.Fatalf("expected fewer messages, got %d", len(res.Messages))
	}
	if res.BytesSaved <= 0 {
		t.Fatalf("expected bytes saved > 0, got %d", res.BytesSaved)
	}
}

func TestDropOldestStrategy_NoOpWhenUnderTarget(t *testing.T) {
	s := compaction.NewDropOldestStrategy()
	msgs := []agentgraph.Message{
		{Role: "user", Content: "hi"},
	}
	res, err := s.Compact(context.Background(), compaction.ContextSlice{
		Messages:     msgs,
		TargetTokens: 1000,
	}, compaction.CompactOpts{})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(res.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(res.Messages))
	}
	if res.BytesSaved != 0 {
		t.Fatalf("expected 0 bytes saved, got %d", res.BytesSaved)
	}
}

func TestDropOldestStrategy_KeepsFloor(t *testing.T) {
	s := compaction.NewDropOldestStrategy()
	msgs := []agentgraph.Message{
		{Role: "user", Content: strings.Repeat("a", 100)},
		{Role: "assistant", Content: strings.Repeat("b", 100)},
	}
	res, err := s.Compact(context.Background(), compaction.ContextSlice{
		Messages:     msgs,
		TargetTokens: 1, // way below current
	}, compaction.CompactOpts{DropOldestKeepRecentN: 2})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(res.Messages) != 2 {
		t.Fatalf("expected floor to keep 2 messages, got %d", len(res.Messages))
	}
}

// TestDropOldestStrategy_ZeroTargetIsNoOp locks in the
// compaction-convergence-01PMDL05 WP02 fix: TargetTokens<=0 means "no
// specific target" (see ContextSlice's doc comment), and must be a
// pure no-op rather than an unconditional trim to the keep-floor. This
// is the exact shape of the pre-fix conflation bug — exec_compute.go
// used to pass TargetTokens: a.MaxTokens (a node's small output-token
// cap, e.g. 256), which read as "wildly over budget" for any
// non-trivial history and drove this strategy to floor-trim on every
// LLM call. Post-fix, exec_compute.go passes TargetTokens: 0 until a
// real context-window budget is threaded through, and this guard is
// what makes that zero value safe.
func TestDropOldestStrategy_ZeroTargetIsNoOp(t *testing.T) {
	s := compaction.NewDropOldestStrategy()
	msgs := []agentgraph.Message{
		{Role: "user", Content: strings.Repeat("a", 100)},
		{Role: "assistant", Content: strings.Repeat("b", 100)},
		{Role: "user", Content: strings.Repeat("c", 100)},
		{Role: "assistant", Content: strings.Repeat("d", 100)},
	}
	res, err := s.Compact(context.Background(), compaction.ContextSlice{
		Messages:     msgs,
		TargetTokens: 0,
	}, compaction.CompactOpts{DropOldestKeepRecentN: 2})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(res.Messages) != len(msgs) {
		t.Fatalf("expected zero-target no-op to keep all %d messages, got %d", len(msgs), len(res.Messages))
	}
	for i := range msgs {
		if res.Messages[i].Content != msgs[i].Content {
			t.Fatalf("message %d mutated by zero-target Compact: got %q want %q", i, res.Messages[i].Content, msgs[i].Content)
		}
	}
	if res.BytesSaved != 0 {
		t.Fatalf("expected 0 bytes saved for zero-target no-op, got %d", res.BytesSaved)
	}
}

// toolPairInvariant asserts the pair-aware invariant DropOldestStrategy
// must uphold: every surviving tool-role message's ToolCallID resolves
// to an assistant ToolCalls entry in the same slice, and every
// surviving assistant ToolCalls entry has a matching tool-role result.
func toolPairInvariant(t *testing.T, msgs []agentgraph.Message) {
	t.Helper()
	toolUseIDs := map[string]bool{}
	for _, m := range msgs {
		if m.Role == "assistant" {
			for _, tc := range m.ToolCalls {
				if tc.ID != "" {
					toolUseIDs[tc.ID] = true
				}
			}
		}
	}
	resultIDs := map[string]bool{}
	for _, m := range msgs {
		if m.Role == "tool" {
			if !toolUseIDs[m.ToolCallID] {
				t.Fatalf("surviving tool_result %q has no matching tool_use in output", m.ToolCallID)
			}
			resultIDs[m.ToolCallID] = true
		}
	}
	for id := range toolUseIDs {
		if !resultIDs[id] {
			t.Fatalf("surviving assistant tool_use %q has no matching tool_result in output", id)
		}
	}
}

// TestDropOldestStrategy_ToolPairAware_NoDanglingResults reproduces the
// hard-blocker shape from llm_provider_adapter.go: the oldest surviving
// messages after a front-to-back trim are frequently an assistant
// tool_use immediately followed by its tool-role tool_result. Naive
// message-at-a-time trimming can drop one half of the pair and strand
// the other, which upstream OpenAI-compat providers 5xx on. This test
// fails against the pre-fix implementation (plain `out = out[1:]`).
func TestDropOldestStrategy_ToolPairAware_NoDanglingResults(t *testing.T) {
	s := compaction.NewDropOldestStrategy()
	msgs := []agentgraph.Message{
		{Role: "user", Content: strings.Repeat("u", 60)},
		{Role: "assistant", Content: strings.Repeat("a", 60), ToolCalls: []agentgraph.ToolCallRequest{{ID: "A", Name: "toolA"}}},
		{Role: "tool", Content: strings.Repeat("r", 60), ToolCallID: "A"},
		{Role: "user", Content: strings.Repeat("u", 60)},
		{Role: "assistant", Content: strings.Repeat("a", 60), ToolCalls: []agentgraph.ToolCallRequest{{ID: "B", Name: "toolB"}}},
		{Role: "tool", Content: strings.Repeat("r", 60), ToolCallID: "B"},
		{Role: "user", Content: strings.Repeat("u", 60)},
	}
	res, err := s.Compact(context.Background(), compaction.ContextSlice{
		Messages:     msgs,
		TargetTokens: 20, // ≈ 80 bytes — forces trimming down near the floor
	}, compaction.CompactOpts{DropOldestKeepRecentN: 2})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(res.Messages) >= len(msgs) {
		t.Fatalf("expected trimming to occur, got %d messages (input had %d)", len(res.Messages), len(msgs))
	}
	toolPairInvariant(t, res.Messages)
}

// TestDropOldestStrategy_MultiToolCallTurnDroppedAsUnit verifies that an
// assistant turn carrying multiple tool calls is dropped (or kept) as
// one atomic unit together with all of its results — never partially.
func TestDropOldestStrategy_MultiToolCallTurnDroppedAsUnit(t *testing.T) {
	s := compaction.NewDropOldestStrategy()
	msgs := []agentgraph.Message{
		{Role: "user", Content: strings.Repeat("u", 40)},
		{
			Role:    "assistant",
			Content: strings.Repeat("a", 40),
			ToolCalls: []agentgraph.ToolCallRequest{
				{ID: "A", Name: "toolA"},
				{ID: "B", Name: "toolB"},
			},
		},
		{Role: "tool", Content: strings.Repeat("r", 40), ToolCallID: "A"},
		{Role: "tool", Content: strings.Repeat("r", 40), ToolCallID: "B"},
		{Role: "user", Content: strings.Repeat("u", 200)},
		{Role: "assistant", Content: strings.Repeat("a", 200)},
	}
	res, err := s.Compact(context.Background(), compaction.ContextSlice{
		Messages:     msgs,
		TargetTokens: 110, // ≈ 440 bytes — big enough to keep the tail, forces the multi-tool-call unit out
	}, compaction.CompactOpts{DropOldestKeepRecentN: 2})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	toolPairInvariant(t, res.Messages)

	// The multi-tool-call unit (assistant + 2 results = 3 messages) must
	// be dropped or kept as a whole — never partially.
	var haveAssistant, haveA, haveB bool
	for _, m := range res.Messages {
		if m.Role == "assistant" {
			for _, tc := range m.ToolCalls {
				if tc.ID == "A" || tc.ID == "B" {
					haveAssistant = true
				}
			}
		}
		if m.Role == "tool" && m.ToolCallID == "A" {
			haveA = true
		}
		if m.Role == "tool" && m.ToolCallID == "B" {
			haveB = true
		}
	}
	if haveAssistant != haveA || haveA != haveB {
		t.Fatalf("multi-tool-call unit split: assistant=%v resultA=%v resultB=%v", haveAssistant, haveA, haveB)
	}
	// This scenario is specifically constructed so the unit is fully
	// dropped (its 3 messages are much cheaper to shed than keeping
	// them, and the target only leaves room for the tail).
	if haveAssistant || haveA || haveB {
		t.Fatalf("expected the oldest multi-tool-call unit to be fully dropped, but part of it survived")
	}
}

// TestDropOldestStrategy_KeepFloorDoesNotSplitPair locks in that the
// keep-recent-N floor rounds outward to a whole unit rather than
// landing mid-pair. With keep=1 and a target far below the input size,
// naive per-message trimming would stop at exactly 1 surviving
// message — which, given this input, would be a lone tool_result with
// no tool_use. The fix must keep the whole trailing pair instead.
func TestDropOldestStrategy_KeepFloorDoesNotSplitPair(t *testing.T) {
	s := compaction.NewDropOldestStrategy()
	msgs := []agentgraph.Message{
		{Role: "user", Content: "hi"},
		{Role: "user", Content: "hi again"},
		{Role: "assistant", Content: strings.Repeat("a", 100), ToolCalls: []agentgraph.ToolCallRequest{{ID: "C", Name: "toolC"}}},
		{Role: "tool", Content: strings.Repeat("r", 100), ToolCallID: "C"},
	}
	res, err := s.Compact(context.Background(), compaction.ContextSlice{
		Messages:     msgs,
		TargetTokens: 1, // far below current size — pressure to trim to the floor
	}, compaction.CompactOpts{DropOldestKeepRecentN: 1})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	toolPairInvariant(t, res.Messages)
	if len(res.Messages) != 2 {
		t.Fatalf("expected the floor to expand to keep the whole trailing pair (2 messages), got %d", len(res.Messages))
	}
	if res.Messages[0].Role != "assistant" || res.Messages[1].Role != "tool" {
		t.Fatalf("expected surviving pair to be [assistant, tool], got %+v", res.Messages)
	}
}

// TestDropOldestStrategy_NonToolHistoryUnchanged is a regression guard:
// message histories with no tool_use/tool_result pairing must trim
// exactly as before (unit-of-one per message).
func TestDropOldestStrategy_NonToolHistoryUnchanged(t *testing.T) {
	s := compaction.NewDropOldestStrategy()
	msgs := []agentgraph.Message{
		{Role: "user", Content: strings.Repeat("a", 80)},
		{Role: "assistant", Content: strings.Repeat("b", 80)},
		{Role: "user", Content: strings.Repeat("c", 80)},
		{Role: "assistant", Content: strings.Repeat("d", 80)},
	}
	res, err := s.Compact(context.Background(), compaction.ContextSlice{
		Messages:     msgs,
		TargetTokens: 30, // ≈ 120 bytes
	}, compaction.CompactOpts{})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(res.Messages) != 2 {
		t.Fatalf("expected front-to-back trim to leave the last 2 messages, got %d", len(res.Messages))
	}
	if res.Messages[0].Content != msgs[2].Content || res.Messages[1].Content != msgs[3].Content {
		t.Fatalf("unexpected surviving messages: %+v", res.Messages)
	}
}

// ---- SummaryStrategy ----

func TestSummaryStrategy_UsesLLM(t *testing.T) {
	llm := &fakeLLM{resp: "concise summary text"}
	s := compaction.NewSummaryStrategy(llm)
	msgs := []agentgraph.Message{
		{Role: "user", Content: "tell me about whales"},
		{Role: "assistant", Content: "whales are mammals that live in oceans"},
	}
	res, err := s.Compact(context.Background(), compaction.ContextSlice{Messages: msgs}, compaction.CompactOpts{})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(res.Messages) != 1 {
		t.Fatalf("expected 1 summary message, got %d", len(res.Messages))
	}
	if !strings.Contains(res.Messages[0].Content, "concise summary text") {
		t.Fatalf("summary content not propagated: %q", res.Messages[0].Content)
	}
	if llm.seen.SystemPrompt == "" {
		t.Fatalf("expected default system prompt to be set")
	}
}

func TestSummaryStrategy_FallbackOnNilLLM(t *testing.T) {
	s := compaction.NewSummaryStrategy(nil)
	msgs := []agentgraph.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
	}
	res, err := s.Compact(context.Background(), compaction.ContextSlice{Messages: msgs}, compaction.CompactOpts{})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(res.Messages) != 1 {
		t.Fatalf("expected fallback to produce 1 summary, got %d", len(res.Messages))
	}
}

func TestSummaryStrategy_FallbackDisabledReturnsErr(t *testing.T) {
	s := compaction.NewSummaryStrategy(nil)
	s.FallbackEnabled = false
	msgs := []agentgraph.Message{{Role: "user", Content: "hi"}}
	_, err := s.Compact(context.Background(), compaction.ContextSlice{Messages: msgs}, compaction.CompactOpts{})
	if !errors.Is(err, compaction.ErrNoLLMSummarizer) {
		t.Fatalf("expected ErrNoLLMSummarizer, got %v", err)
	}
}

func TestSummaryStrategy_LLMErrorFallsBackByDefault(t *testing.T) {
	llm := &fakeLLM{err: errors.New("llm down")}
	s := compaction.NewSummaryStrategy(llm)
	msgs := []agentgraph.Message{{Role: "user", Content: "x"}}
	res, err := s.Compact(context.Background(), compaction.ContextSlice{Messages: msgs}, compaction.CompactOpts{})
	if err != nil {
		t.Fatalf("expected fallback, got %v", err)
	}
	if len(res.Messages) != 1 {
		t.Fatalf("expected 1 fallback summary, got %d", len(res.Messages))
	}
}

// ---- SemanticClusterStrategy ----

func TestSemanticClusterStrategy_KeepsRepresentativePerCluster(t *testing.T) {
	emb := &fakeEmbedder{dims: 8}
	s := compaction.NewSemanticClusterStrategy(emb)
	msgs := []agentgraph.Message{
		{Role: "user", Content: "alpha"},
		{Role: "assistant", Content: "alpha-resp"},
		{Role: "user", Content: "beta"},
		{Role: "assistant", Content: "beta-resp"},
		{Role: "user", Content: "gamma"},
		{Role: "assistant", Content: "gamma-resp"},
	}
	res, err := s.Compact(context.Background(), compaction.ContextSlice{Messages: msgs}, compaction.CompactOpts{
		SemanticClusterCount: 3,
	})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(res.Messages) > 3 {
		t.Fatalf("expected ≤3 messages, got %d", len(res.Messages))
	}
	if len(res.Messages) == 0 {
		t.Fatalf("expected at least 1 message")
	}
}

func TestSemanticClusterStrategy_FallbackOnNilEmbedder(t *testing.T) {
	s := compaction.NewSemanticClusterStrategy(nil)
	msgs := []agentgraph.Message{
		{Role: "user", Content: "a"},
		{Role: "assistant", Content: "b"},
		{Role: "user", Content: "c"},
		{Role: "assistant", Content: "d"},
	}
	res, err := s.Compact(context.Background(), compaction.ContextSlice{Messages: msgs}, compaction.CompactOpts{
		SemanticClusterCount: 2,
	})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(res.Messages) > len(msgs) {
		t.Fatalf("expected to compact via stride, got %d", len(res.Messages))
	}
}

func TestSemanticClusterStrategy_FallbackDisabledReturnsErr(t *testing.T) {
	s := compaction.NewSemanticClusterStrategy(nil)
	s.FallbackEnabled = false
	_, err := s.Compact(context.Background(), compaction.ContextSlice{
		Messages: []agentgraph.Message{{Content: "x"}, {Content: "y"}},
	}, compaction.CompactOpts{SemanticClusterCount: 1})
	if !errors.Is(err, compaction.ErrNoEmbedder) {
		t.Fatalf("expected ErrNoEmbedder, got %v", err)
	}
}

// ---- CustomSubgraphStrategy ----

type fakeKernel struct {
	out map[string]agentgraph.PortValues
	err error
}

func (k *fakeKernel) RunGraph(_ context.Context, _ *agentgraph.Graph, _ map[string]agentgraph.PortValues) (map[string]agentgraph.PortValues, error) {
	if k.err != nil {
		return nil, k.err
	}
	return k.out, nil
}

func TestCustomSubgraphStrategy_RunsKernel(t *testing.T) {
	out := []agentgraph.Message{{Role: "system", Content: "compacted!"}}
	k := &fakeKernel{out: map[string]agentgraph.PortValues{
		"leaf": {"messages": out},
	}}
	s := compaction.NewCustomSubgraphStrategy(k)
	graph := &agentgraph.Graph{
		ID:          "g",
		Entrypoints: []string{"leaf"},
	}
	res, err := s.Compact(context.Background(), compaction.ContextSlice{
		Messages: []agentgraph.Message{{Role: "user", Content: "long input"}},
	}, compaction.CompactOpts{CustomGraph: graph})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(res.Messages) != 1 || res.Messages[0].Content != "compacted!" {
		t.Fatalf("custom subgraph output not propagated: %+v", res.Messages)
	}
}

func TestCustomSubgraphStrategy_NilRunner(t *testing.T) {
	s := compaction.NewCustomSubgraphStrategy(nil)
	_, err := s.Compact(context.Background(), compaction.ContextSlice{}, compaction.CompactOpts{
		CustomGraph: &agentgraph.Graph{},
	})
	if !errors.Is(err, compaction.ErrNoKernelRunner) {
		t.Fatalf("expected ErrNoKernelRunner, got %v", err)
	}
}

func TestCustomSubgraphStrategy_MissingGraph(t *testing.T) {
	s := compaction.NewCustomSubgraphStrategy(&fakeKernel{})
	_, err := s.Compact(context.Background(), compaction.ContextSlice{}, compaction.CompactOpts{})
	if !errors.Is(err, compaction.ErrCustomSubgraphMissing) {
		t.Fatalf("expected ErrCustomSubgraphMissing, got %v", err)
	}
}

// TestDropOldestStrategy_NonContiguousPairRepaired closes the gap an
// adversarial review of PR #265 reproduced: unit-grouping establishes
// tool_use/tool_result pairing by contiguity, so a caller that presents
// a real pair non-contiguously (a custom_subgraph strategy, or a
// hand-authored graph wiring the compact node) got the assistant unit
// closed with zero results, the real result dropped as an "orphan", and
// a dangling ToolCalls entry left behind — the exact provider-rejection
// shape this strategy exists to prevent.
//
// repairToolPairs now guarantees the invariant structurally rather than
// by assuming caller behaviour. This test fails without it.
func TestDropOldestStrategy_NonContiguousPairRepaired(t *testing.T) {
	msgs := []agentgraph.Message{
		{Role: "user", Content: strings.Repeat("p", 400)},
		{Role: "assistant", ToolCalls: []agentgraph.ToolCallRequest{{ID: "A"}}, Content: "call A"},
		{Role: "tool", ToolCallID: "C", Content: "interleaved, unrelated"},
		{Role: "tool", ToolCallID: "A", Content: "the real result for A"},
		{Role: "user", Content: "tail"},
	}
	got, err := compaction.NewDropOldestStrategy().Compact(context.Background(), compaction.ContextSlice{
		Messages: msgs, TargetTokens: 5,
	}, compaction.CompactOpts{})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}

	survivingResults := map[string]bool{}
	for _, m := range got.Messages {
		if m.Role == "tool" {
			survivingResults[m.ToolCallID] = true
		}
	}
	for _, m := range got.Messages {
		for _, tc := range m.ToolCalls {
			if !survivingResults[tc.ID] {
				t.Fatalf("surviving tool_call %q has no surviving result — provider would reject this request", tc.ID)
			}
		}
	}
	survivingCalls := map[string]bool{}
	for _, m := range got.Messages {
		for _, tc := range m.ToolCalls {
			survivingCalls[tc.ID] = true
		}
	}
	for _, m := range got.Messages {
		if m.Role == "tool" && !survivingCalls[m.ToolCallID] {
			t.Fatalf("surviving tool_result %q has no originating tool_use", m.ToolCallID)
		}
	}
}
