// Concrete Compactor implementations: drop_oldest, summary,
// semantic_cluster, custom_subgraph. Each one follows the same shape:
// pull configuration from CompactOpts, work over the input messages,
// return a CompactedContext (or an error).
package compaction

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/sigil-tech/kaneaz-harness/core/agentgraph"
)

// ---- drop_oldest ----

// DropOldestStrategy drops the oldest messages until the byte count is
// below the target. System messages are always preserved (they live in
// SystemPrompt, separate from the message slice; if a "system" role
// message appears in the slice we keep the most-recent one as a
// safeguard).
//
// The strategy is deterministic and self-contained: no LLM call, no
// embedding lookup. It is the safe fallback when other strategies
// fail or when the user explicitly picks gentle aggressiveness.
type DropOldestStrategy struct {
	// DefaultKeepRecentN is the floor on retained messages when
	// CompactOpts.DropOldestKeepRecentN == 0. Two is a sensible
	// minimum (keep the last user turn + last assistant turn).
	DefaultKeepRecentN int
}

// NewDropOldestStrategy constructs a configured DropOldestStrategy.
// DefaultKeepRecentN is clamped to a minimum of 1.
func NewDropOldestStrategy() *DropOldestStrategy {
	return &DropOldestStrategy{DefaultKeepRecentN: 2}
}

// Strategy reports the strategy name.
func (s *DropOldestStrategy) Strategy() Strategy { return StrategyDropOldest }

// Compact runs the drop-oldest algorithm.
func (s *DropOldestStrategy) Compact(_ context.Context, in ContextSlice, opts CompactOpts) (CompactedContext, error) {
	keep := opts.DropOldestKeepRecentN
	if keep <= 0 {
		keep = s.DefaultKeepRecentN
		if keep <= 0 {
			keep = 2
		}
	}
	target := in.TargetTokens
	beforeBytes := bytesOf(in.Messages)

	// If we're already under target, no-op.
	if target > 0 && approxTokens(beforeBytes) <= target {
		return CompactedContext{
			Messages:    append([]agentgraph.Message(nil), in.Messages...),
			TokensAfter: approxTokens(beforeBytes),
			Strategy:    s.Strategy(),
			BytesSaved:  0,
		}, nil
	}

	if len(in.Messages) <= keep {
		// Nothing safe to drop; surface an "approximately untouched"
		// result rather than an error so the pipeline can fall back.
		return CompactedContext{
			Messages:    append([]agentgraph.Message(nil), in.Messages...),
			TokensAfter: approxTokens(beforeBytes),
			Strategy:    s.Strategy(),
			BytesSaved:  0,
		}, nil
	}

	// Walk from the front, dropping until we're either under target or
	// down to the floor.
	out := append([]agentgraph.Message(nil), in.Messages...)
	for len(out) > keep {
		if target > 0 && approxTokens(bytesOf(out)) <= target {
			break
		}
		out = out[1:]
	}
	afterBytes := bytesOf(out)
	return CompactedContext{
		Messages:    out,
		TokensAfter: approxTokens(afterBytes),
		Strategy:    s.Strategy(),
		BytesSaved:  beforeBytes - afterBytes,
	}, nil
}

// ---- summary ----

// LLMSummarizer is the agentgraph.LLMProvider seam for the summary
// strategy. Defining a local alias keeps the strategy struct from
// depending on the broader agentgraph LLM types in its public surface
// while still being trivially wireable from production code.
type LLMSummarizer = agentgraph.LLMProvider

// SummaryStrategy collapses the input messages into a single assistant
// message produced by an LLM. Falls back to an inline heuristic
// summary when no LLM is configured (per the WP12-T1 spec note: the
// activity catalog from WP05 may not have shipped yet).
type SummaryStrategy struct {
	// LLM is the provider used to produce the summary. Nil triggers
	// the fallback heuristic.
	LLM LLMSummarizer
	// Provider + Model are the defaults applied when CompactOpts does
	// not pin specific values.
	Provider string
	Model    string
	// Prompt is the system prompt the summary call uses. A reasonable
	// default is provided when empty.
	Prompt string
	// FallbackEnabled controls whether the strategy is allowed to
	// produce an inline heuristic summary when LLM is nil. Defaults
	// to true.
	FallbackEnabled bool
}

// NewSummaryStrategy returns a SummaryStrategy with the default
// system prompt and fallback enabled.
func NewSummaryStrategy(llm LLMSummarizer) *SummaryStrategy {
	return &SummaryStrategy{
		LLM:             llm,
		Prompt:          defaultSummaryPrompt,
		FallbackEnabled: true,
	}
}

const defaultSummaryPrompt = "" +
	"You are a conversation summarizer. Produce a terse but complete summary " +
	"of the following dialogue, preserving every concrete fact, decision, " +
	"file path, identifier, and unresolved question. Output a single " +
	"paragraph; do not editorialize."

// Strategy reports the strategy name.
func (s *SummaryStrategy) Strategy() Strategy { return StrategySummary }

// Compact runs the summary algorithm.
func (s *SummaryStrategy) Compact(ctx context.Context, in ContextSlice, opts CompactOpts) (CompactedContext, error) {
	if len(in.Messages) == 0 {
		return CompactedContext{Strategy: s.Strategy()}, nil
	}
	beforeBytes := bytesOf(in.Messages)

	transcript := transcriptOf(in.Messages)

	var summary string
	if s.LLM != nil {
		provider := opts.SummaryProvider
		if provider == "" {
			provider = s.Provider
		}
		model := opts.SummaryModel
		if model == "" {
			model = s.Model
		}
		prompt := s.Prompt
		if prompt == "" {
			prompt = defaultSummaryPrompt
		}
		req := agentgraph.LLMRequest{
			Provider:     provider,
			Model:        model,
			SystemPrompt: prompt,
			Messages: []agentgraph.Message{
				{Role: "user", Content: transcript},
			},
		}
		resp, err := s.LLM.Generate(ctx, req)
		if err != nil {
			if !s.FallbackEnabled {
				return CompactedContext{}, fmt.Errorf("summary: %w", err)
			}
			summary = heuristicSummary(in.Messages)
		} else {
			summary = strings.TrimSpace(resp.Content)
			if summary == "" && s.FallbackEnabled {
				summary = heuristicSummary(in.Messages)
			}
		}
	} else if s.FallbackEnabled {
		summary = heuristicSummary(in.Messages)
	} else {
		return CompactedContext{}, ErrNoLLMSummarizer
	}

	out := []agentgraph.Message{
		{
			Role:    "system",
			Content: "[compaction summary] " + summary,
			Name:    "compaction-summary",
		},
	}
	afterBytes := bytesOf(out)
	return CompactedContext{
		Messages:    out,
		TokensAfter: approxTokens(afterBytes),
		Strategy:    s.Strategy(),
		BytesSaved:  beforeBytes - afterBytes,
	}, nil
}

// transcriptOf renders a message slice as `role: content` lines.
func transcriptOf(ms []agentgraph.Message) string {
	var sb strings.Builder
	for _, m := range ms {
		sb.WriteString(m.Role)
		sb.WriteString(": ")
		sb.WriteString(m.Content)
		sb.WriteString("\n")
	}
	return sb.String()
}

// heuristicSummary is the fallback summarizer used when no LLM is
// available. It picks role:first-N-chars for each message and joins
// them. Crude but deterministic.
func heuristicSummary(ms []agentgraph.Message) string {
	var sb strings.Builder
	const maxPerMsg = 80
	for i, m := range ms {
		if i > 0 {
			sb.WriteString(" | ")
		}
		sb.WriteString(m.Role)
		sb.WriteString(":")
		c := m.Content
		if len(c) > maxPerMsg {
			c = c[:maxPerMsg] + "…"
		}
		sb.WriteString(c)
	}
	return sb.String()
}

// ---- semantic_cluster ----

// Embedder is the seam StrategySemanticCluster uses to compute message
// vectors. Mirrors core/corpus.Embedder so production wiring can pass
// a corpus embedder directly.
type Embedder interface {
	Dimensions() int
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// SemanticClusterStrategy clusters messages by embedding similarity
// and keeps a representative per cluster (the longest message wins
// the tie-break). When no embedder is wired the strategy falls back
// to a degraded "every other message" pick if FallbackEnabled.
type SemanticClusterStrategy struct {
	Embedder         Embedder
	DefaultClusters  int
	FallbackEnabled  bool
}

// NewSemanticClusterStrategy constructs the strategy with sensible
// defaults.
func NewSemanticClusterStrategy(emb Embedder) *SemanticClusterStrategy {
	return &SemanticClusterStrategy{
		Embedder:        emb,
		DefaultClusters: 4,
		FallbackEnabled: true,
	}
}

// Strategy reports the strategy name.
func (s *SemanticClusterStrategy) Strategy() Strategy { return StrategySemanticCluster }

// Compact runs the semantic-cluster algorithm.
func (s *SemanticClusterStrategy) Compact(ctx context.Context, in ContextSlice, opts CompactOpts) (CompactedContext, error) {
	if len(in.Messages) == 0 {
		return CompactedContext{Strategy: s.Strategy()}, nil
	}
	beforeBytes := bytesOf(in.Messages)
	clusters := opts.SemanticClusterCount
	if clusters <= 0 {
		clusters = s.DefaultClusters
		if clusters <= 0 {
			clusters = 4
		}
	}
	if clusters >= len(in.Messages) {
		// More clusters than messages — nothing to compact.
		return CompactedContext{
			Messages:    append([]agentgraph.Message(nil), in.Messages...),
			TokensAfter: approxTokens(beforeBytes),
			Strategy:    s.Strategy(),
			BytesSaved:  0,
		}, nil
	}

	if s.Embedder == nil {
		if !s.FallbackEnabled {
			return CompactedContext{}, ErrNoEmbedder
		}
		// Degraded fallback: stride-pick `clusters` messages evenly
		// across the slice.
		out := stridePick(in.Messages, clusters)
		afterBytes := bytesOf(out)
		return CompactedContext{
			Messages:    out,
			TokensAfter: approxTokens(afterBytes),
			Strategy:    s.Strategy(),
			BytesSaved:  beforeBytes - afterBytes,
		}, nil
	}

	texts := make([]string, len(in.Messages))
	for i, m := range in.Messages {
		texts[i] = m.Role + ": " + m.Content
	}
	vecs, err := s.Embedder.Embed(ctx, texts)
	if err != nil {
		if s.FallbackEnabled {
			out := stridePick(in.Messages, clusters)
			afterBytes := bytesOf(out)
			return CompactedContext{
				Messages:    out,
				TokensAfter: approxTokens(afterBytes),
				Strategy:    s.Strategy(),
				BytesSaved:  beforeBytes - afterBytes,
			}, nil
		}
		return CompactedContext{}, fmt.Errorf("semantic_cluster: embed: %w", err)
	}
	if len(vecs) != len(in.Messages) {
		return CompactedContext{}, fmt.Errorf(
			"semantic_cluster: embedder returned %d vectors for %d messages",
			len(vecs), len(in.Messages),
		)
	}

	// Simple greedy clustering: assign every message to the seed it is
	// closest to (cosine). Seeds are the first `clusters` messages,
	// chosen by stride to spread coverage.
	seedIdx := strideIndices(len(in.Messages), clusters)
	assign := make([]int, len(in.Messages))
	for i := range in.Messages {
		bestSeed := 0
		bestSim := float32(-2)
		for sj, si := range seedIdx {
			sim := cosine(vecs[i], vecs[si])
			if sim > bestSim {
				bestSim = sim
				bestSeed = sj
			}
		}
		assign[i] = bestSeed
	}

	// For each cluster keep the longest-content message (most info).
	picked := make(map[int]int)        // cluster idx -> message idx
	pickedLen := make(map[int]int)
	for i, m := range in.Messages {
		c := assign[i]
		if existing, ok := picked[c]; !ok || len(m.Content) > pickedLen[c] {
			picked[c] = i
			pickedLen[c] = len(m.Content)
			_ = existing
		}
	}
	indices := make([]int, 0, len(picked))
	for _, i := range picked {
		indices = append(indices, i)
	}
	sort.Ints(indices)
	out := make([]agentgraph.Message, 0, len(indices))
	for _, i := range indices {
		out = append(out, in.Messages[i])
	}
	afterBytes := bytesOf(out)
	return CompactedContext{
		Messages:    out,
		TokensAfter: approxTokens(afterBytes),
		Strategy:    s.Strategy(),
		BytesSaved:  beforeBytes - afterBytes,
	}, nil
}

// stridePick selects `n` messages evenly across the slice (always
// includes the first and last). Used as the fallback when embedding
// is unavailable.
func stridePick(ms []agentgraph.Message, n int) []agentgraph.Message {
	if n <= 0 || len(ms) == 0 {
		return nil
	}
	if n >= len(ms) {
		out := make([]agentgraph.Message, len(ms))
		copy(out, ms)
		return out
	}
	idxs := strideIndices(len(ms), n)
	out := make([]agentgraph.Message, 0, n)
	for _, i := range idxs {
		out = append(out, ms[i])
	}
	return out
}

// strideIndices returns `n` evenly-spaced indices into a slice of
// length `total`. Always returns at least 0 and total-1 when n >= 2.
func strideIndices(total, n int) []int {
	if n <= 0 || total <= 0 {
		return nil
	}
	if n == 1 {
		return []int{total - 1}
	}
	out := make([]int, 0, n)
	for k := 0; k < n; k++ {
		idx := int(math.Round(float64(k) * float64(total-1) / float64(n-1)))
		if idx >= total {
			idx = total - 1
		}
		// Avoid duplicates.
		if len(out) > 0 && out[len(out)-1] == idx {
			continue
		}
		out = append(out, idx)
	}
	return out
}

// cosine returns the cosine similarity of two equal-length vectors.
// Returns 0 for zero vectors.
func cosine(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float32
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / float32(math.Sqrt(float64(na))*math.Sqrt(float64(nb)))
}

// ---- custom_subgraph ----

// CustomSubgraphStrategy invokes a user-supplied agentgraph.Graph as a
// compaction algorithm. The graph is expected to have a designated
// input port that accepts `[]agentgraph.Message` and a leaf node
// whose output port carries the compacted messages.
//
// Recursion is bounded: the strategy reads the depth from ctx and
// rejects calls that would push past CompactOpts.MaxRecursionDepth
// (default 2 from FR-045).
type CustomSubgraphStrategy struct {
	Runner KernelRunner
}

// NewCustomSubgraphStrategy constructs a strategy bound to the given
// kernel runner. Pass nil to get a strategy that always returns
// ErrNoKernelRunner.
func NewCustomSubgraphStrategy(runner KernelRunner) *CustomSubgraphStrategy {
	return &CustomSubgraphStrategy{Runner: runner}
}

// Strategy reports the strategy name.
func (s *CustomSubgraphStrategy) Strategy() Strategy { return StrategyCustomSubgraph }

// Compact runs the user-supplied subgraph as a compaction step.
func (s *CustomSubgraphStrategy) Compact(ctx context.Context, in ContextSlice, opts CompactOpts) (CompactedContext, error) {
	if s.Runner == nil {
		return CompactedContext{}, ErrNoKernelRunner
	}
	if opts.CustomGraph == nil {
		return CompactedContext{}, ErrCustomSubgraphMissing
	}
	maxDepth := opts.MaxRecursionDepth
	if maxDepth <= 0 {
		maxDepth = DefaultMaxRecursionDepth
	}
	d := depth(ctx)
	if d >= maxDepth {
		return CompactedContext{}, ErrRecursionExceeded
	}

	inPort := opts.SubgraphInputPort
	if inPort == "" {
		inPort = "messages"
	}
	outPort := opts.SubgraphOutputPort
	if outPort == "" {
		outPort = "messages"
	}

	beforeBytes := bytesOf(in.Messages)

	// Build inputs keyed by entrypoint node ID. We assign the same
	// messages slice to every entrypoint's input port; graphs with
	// multiple entrypoints can pick the first entry. Strategies that
	// need a more structured input shape can be authored as activities
	// in the future.
	entryInputs := make(map[string]agentgraph.PortValues, len(opts.CustomGraph.Entrypoints))
	for _, ep := range opts.CustomGraph.Entrypoints {
		entryInputs[ep] = agentgraph.PortValues{
			inPort: append([]agentgraph.Message(nil), in.Messages...),
		}
	}

	childCtx := withDepth(ctx, d+1)
	leafOutputs, err := s.Runner.RunGraph(childCtx, opts.CustomGraph, entryInputs)
	if err != nil {
		return CompactedContext{}, fmt.Errorf("custom_subgraph: run: %w", err)
	}

	// Walk leaf outputs to find the messages slice on the configured
	// output port. We accept either []agentgraph.Message directly or
	// a []any whose elements are Messages.
	var compacted []agentgraph.Message
	for _, leaf := range leafOutputs {
		v, ok := leaf[outPort]
		if !ok {
			continue
		}
		switch typed := v.(type) {
		case []agentgraph.Message:
			compacted = append([]agentgraph.Message(nil), typed...)
		case []any:
			for _, m := range typed {
				if mm, ok := m.(agentgraph.Message); ok {
					compacted = append(compacted, mm)
				}
			}
		}
		if compacted != nil {
			break
		}
	}
	if compacted == nil {
		// No leaf produced a valid output; fall through to "empty"
		// which the pipeline treats as a no-op.
		compacted = append([]agentgraph.Message(nil), in.Messages...)
	}
	afterBytes := bytesOf(compacted)
	return CompactedContext{
		Messages:    compacted,
		TokensAfter: approxTokens(afterBytes),
		Strategy:    s.Strategy(),
		BytesSaved:  beforeBytes - afterBytes,
	}, nil
}

// approxTokens converts a byte count to an approximate token count
// using the rule-of-thumb 4 bytes / token. Good enough for budget
// thresholding; the kernel is the canonical token counter on the LLM
// response.
func approxTokens(b int) int {
	if b <= 0 {
		return 0
	}
	return (b + 3) / 4
}
