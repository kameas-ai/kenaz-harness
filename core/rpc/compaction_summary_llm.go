package rpc

// compaction_summary_llm.go — chat-turn-integrity-01PMZ606 WP08: a real
// LLM summariser for the compaction "summary" strategy.
//
// Before this, the only production registration of the strategy the
// panel labels "Summary (LLM)" (CompactionStrategyPanel.vue) was
// `compaction.NewSummaryStrategy(nil)` (core/rpc/api.go's
// newGraphManagerWithDeps), so every run fell through to
// SummaryStrategy's inline heuristicSummary — an 80-char-per-message
// pipe join, never a model call.
//
// The audit's prescribed wire is refuted twice over (spec.md §5.6):
// `compactionwiring.LLMCaller` (core/agentgraph/compaction/wiring/llm.go)
// exposes `CallForSummary(ctx, model, systemPrompt, userPrompt) (string,
// int, int, error)`, not `Generate(ctx, LLMRequest) (LLMResponse,
// error)` — it does not satisfy agentgraph.LLMProvider
// (compaction.LLMSummarizer's alias target). And the only production
// implementation of LLMProvider, chat.LLMProviderAdapter, is constructed
// per-run (one profileID/modelOverride pair per chat turn), while the
// compaction pipeline's registered strategies are process-scoped and
// outlive any one run. Neither existing type can be handed to
// NewSummaryStrategy directly.
//
// compactionSummaryLLM is the new adapter this WP writes: it satisfies
// agentgraph.LLMProvider by translating to/from LLMCaller's shape, so it
// gets LLMCaller's registry plumbing (profile resolution, retry, audit,
// cost tagging via cost.KindCompaction) for free instead of
// re-implementing it.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/agentgraph/compaction"
	compactionwiring "github.com/kameas-ai/kenaz-harness/core/agentgraph/compaction/wiring"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// errNoCompactionSummaryModel is returned by compactionSummaryLLM.Generate
// when it cannot resolve a provider/model pair to call — the request
// carried none and defaultModel (Settings.CompactionModel, the same field
// the chat compaction dial's pickModel() prefers — session_compaction.go)
// is unset or empty. SummaryStrategy's FallbackEnabled path (the default,
// and the only production configuration: see NewSummaryStrategy) catches
// this typed error and produces a heuristic summary instead of failing
// compaction outright — "a summariser that hard-fails compaction is worse
// than a heuristic one" (tasks.md WP08).
var errNoCompactionSummaryModel = errors.New("compaction: no summarization model configured")

// compactionSummaryLLM adapts compactionwiring.LLMCaller onto
// agentgraph.LLMProvider (compaction.LLMSummarizer) for the "summary"
// strategy's process-scoped registration on the base pipeline.
//
// Process-scoped, not per-run: registerManualCompactionStrategies
// constructs one of these at boot (well after the registry exists — see
// that function's doc comment for why this can't be done any earlier)
// and it outlives every chat turn, so Generate resolves the provider and
// model to call on EVERY invocation rather than once at construction.
// LLMRequest.Provider/Model — set by SummaryStrategy from its own
// Provider/Model fields or a manual-trigger CompactOpts override — win
// when supplied; otherwise defaultModel is consulted.
type compactionSummaryLLM struct {
	caller *compactionwiring.LLMCaller
	// defaultModel resolves the settings-configured compaction model
	// (deps.CompactionModel, i.e. Settings.CompactionModel) when the
	// request carries no explicit provider/model. nil or ok=false means
	// "no default configured."
	defaultModel func() (compaction.ProviderProfileRef, bool)
}

// newCompactionSummaryLLM constructs the adapter. Returns nil when reg is
// nil (the nil-chassis test/boot path — compactionwiring.NewLLMCaller
// itself returns nil for a nil registry, "callers MUST nil-check before
// binding") so the caller can skip registration entirely and leave the
// earlier nil-LLM heuristic-fallback strategy from newGraphManagerWithDeps
// in place, rather than registering an adapter that can only ever return
// errNoCompactionSummaryModel.
func newCompactionSummaryLLM(reg corellm.Registry, defaultModel func() (compaction.ProviderProfileRef, bool)) *compactionSummaryLLM {
	caller := compactionwiring.NewLLMCaller(reg)
	if caller == nil {
		return nil
	}
	return &compactionSummaryLLM{caller: caller, defaultModel: defaultModel}
}

// Generate satisfies agentgraph.LLMProvider (compaction.LLMSummarizer).
func (a *compactionSummaryLLM) Generate(ctx context.Context, req agentgraph.LLMRequest) (agentgraph.LLMResponse, error) {
	if a == nil || a.caller == nil {
		return agentgraph.LLMResponse{}, errNoCompactionSummaryModel
	}
	ref := compaction.ProviderProfileRef{ProviderID: req.Provider, ModelID: req.Model}
	if ref.ProviderID == "" && a.defaultModel != nil {
		if def, ok := a.defaultModel(); ok && def.ProviderID != "" {
			ref = def
		}
	}
	if ref.ProviderID == "" {
		return agentgraph.LLMResponse{}, errNoCompactionSummaryModel
	}
	text, inTok, outTok, err := a.caller.CallForSummary(ctx, ref, req.SystemPrompt, summaryUserPrompt(req.Messages))
	if err != nil {
		return agentgraph.LLMResponse{}, fmt.Errorf("compaction summary llm: %w", err)
	}
	return agentgraph.LLMResponse{
		Content:    text,
		TokensUsed: inTok + outTok,
	}, nil
}

// summaryUserPrompt renders SummaryStrategy's request messages as the
// single user-turn string CallForSummary expects. SummaryStrategy's
// Compact (core/agentgraph/compaction/strategies.go) always sends exactly
// one {Role: "user", Content: transcript} message, but this defensively
// joins multiple in case a future caller changes that, rather than
// silently dropping all but the first.
func summaryUserPrompt(msgs []agentgraph.Message) string {
	if len(msgs) == 1 {
		return msgs[0].Content
	}
	var sb strings.Builder
	for i, m := range msgs {
		if i > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString(m.Content)
	}
	return sb.String()
}
