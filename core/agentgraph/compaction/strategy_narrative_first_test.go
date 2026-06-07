package compaction

import (
	"context"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph"
)

// ---- fake NarrativeLookup ----

type fakeNarrativeLookup struct {
	content string
	err     error
}

func (f *fakeNarrativeLookup) FindNarrative(_ context.Context, _ string, _ []string) (string, error) {
	return f.content, f.err
}

func TestNarrativeFirstStrategy_WithNarrative(t *testing.T) {
	t.Parallel()
	lookup := &fakeNarrativeLookup{content: "narrative content about the session"}
	strat := NewNarrativeFirstStrategy("sess-1", lookup)

	in := ContextSlice{
		Messages: []agentgraph.Message{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "world with a lot of context that should be compressed away"},
		},
		TargetTokens: 100,
	}
	out, err := strat.Compact(context.Background(), in, CompactOpts{})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	// Should have replaced with single narrative message.
	if len(out.Messages) != 1 {
		t.Errorf("len(Messages) = %d, want 1", len(out.Messages))
	}
	if out.Messages[0].Role != "system" {
		t.Errorf("Role = %q, want system", out.Messages[0].Role)
	}
	if !strings.Contains(out.Messages[0].Content, "[narrative]") {
		t.Errorf("Content should contain [narrative] tag, got %q", out.Messages[0].Content)
	}
	if out.Strategy != StrategyNarrativeFirst {
		t.Errorf("Strategy = %q, want narrative_first", out.Strategy)
	}
	if out.BytesSaved <= 0 {
		t.Errorf("BytesSaved = %d, want > 0", out.BytesSaved)
	}
}

func TestNarrativeFirstStrategy_WithSystemPrompt(t *testing.T) {
	t.Parallel()
	lookup := &fakeNarrativeLookup{content: "narrative"}
	strat := NewNarrativeFirstStrategy("sess-2", lookup)

	in := ContextSlice{
		Messages:     []agentgraph.Message{{Role: "user", Content: "query"}},
		SystemPrompt: "You are helpful.",
	}
	out, err := strat.Compact(context.Background(), in, CompactOpts{})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	// Should have system prompt + narrative = 2 messages.
	if len(out.Messages) != 2 {
		t.Errorf("len(Messages) = %d, want 2 (system + narrative)", len(out.Messages))
	}
	if out.Messages[0].Content != "You are helpful." {
		t.Errorf("first message should be system prompt, got %q", out.Messages[0].Content)
	}
}

func TestNarrativeFirstStrategy_NoNarrative_PassThrough(t *testing.T) {
	t.Parallel()
	lookup := &fakeNarrativeLookup{content: ""} // no narrative
	strat := NewNarrativeFirstStrategy("sess-3", lookup)

	msgs := []agentgraph.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "world"},
	}
	in := ContextSlice{Messages: msgs}
	out, err := strat.Compact(context.Background(), in, CompactOpts{})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	// Pass-through: same messages, BytesSaved=0.
	if len(out.Messages) != 2 {
		t.Errorf("pass-through: len(Messages) = %d, want 2", len(out.Messages))
	}
	if out.BytesSaved != 0 {
		t.Errorf("pass-through: BytesSaved = %d, want 0", out.BytesSaved)
	}
}

func TestNarrativeFirstStrategy_NilLookup_PassThrough(t *testing.T) {
	t.Parallel()
	strat := NewNarrativeFirstStrategy("sess-4", nil)

	in := ContextSlice{Messages: []agentgraph.Message{{Role: "user", Content: "hello"}}}
	out, err := strat.Compact(context.Background(), in, CompactOpts{})
	if err != nil {
		t.Fatalf("Compact with nil lookup: %v", err)
	}
	if len(out.Messages) != 1 {
		t.Errorf("nil-lookup pass-through: len(Messages) = %d, want 1", len(out.Messages))
	}
}

func TestAllStrategies_ContainsNarrativeFirst(t *testing.T) {
	t.Parallel()
	strategies := AllStrategies()
	for _, s := range strategies {
		if s == StrategyNarrativeFirst {
			return
		}
	}
	t.Error("AllStrategies() does not contain StrategyNarrativeFirst")
}

func TestNarrativeFirstStrategy_StrategyName(t *testing.T) {
	t.Parallel()
	strat := NewNarrativeFirstStrategy("", nil)
	if strat.Strategy() != StrategyNarrativeFirst {
		t.Errorf("Strategy() = %q, want narrative_first", strat.Strategy())
	}
}
