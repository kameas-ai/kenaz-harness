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
