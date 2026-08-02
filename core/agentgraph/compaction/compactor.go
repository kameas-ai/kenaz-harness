// Package compaction implements the configurable compaction subsystem
// (mission agent-kernel-graph; Bundle D — FR-041..FR-045).
//
// Compaction is the kernel-managed mechanism that shrinks the
// ContextGraph or message-history slice when budget pressure rises.
// It runs at three invocation sites — token-budget pre-call, post-tool
// result trim, and manual user trigger — and dispatches to one of four
// strategies — `summary`, `drop_oldest`, `semantic_cluster`, or
// `custom_subgraph`.
//
// The package depends on `core/agentgraph` for value types
// (Message, LLMRequest, Graph) and may depend on `core/llm` for the
// summary strategy and `core/corpus` for the embedding seam used by
// `semantic_cluster`. Crucially the dependency direction is one-way:
// `core/agentgraph` does NOT import this package — instead it consumes
// the `agentgraph.Compactor` interface this package satisfies.
package compaction

import (
	"context"
	"errors"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph"
)

// Site identifies the kernel firing point that triggered a compaction.
type Site string

const (
	// SitePreCall fires before an LLMNode dispatches its request, when
	// the prepared input would exceed the token budget threshold.
	SitePreCall Site = "pre_call"
	// SitePostTool fires after a ToolNode returns, when the result
	// payload exceeds tool_result_max_bytes.
	SitePostTool Site = "post_tool"
	// SiteManual fires from the user-facing manual "compact now" trigger.
	SiteManual Site = "manual"
)

// AllSites enumerates the supported invocation sites.
func AllSites() []Site {
	return []Site{SitePreCall, SitePostTool, SiteManual}
}

// Strategy identifies a compaction algorithm.
type Strategy string

const (
	// StrategySummary uses an LLM to produce a single summary message.
	StrategySummary Strategy = "summary"
	// StrategyDropOldest discards the oldest messages until under
	// target.
	StrategyDropOldest Strategy = "drop_oldest"
	// StrategySemanticCluster uses embeddings to cluster messages and
	// keeps a representative per cluster.
	StrategySemanticCluster Strategy = "semantic_cluster"
	// StrategyCustomSubgraph runs a user-supplied agentgraph.Graph that
	// takes messages and produces compacted messages.
	StrategyCustomSubgraph Strategy = "custom_subgraph"
)

// AllStrategies enumerates the supported strategies.
func AllStrategies() []Strategy {
	return []Strategy{
		StrategyNarrativeFirst, // memory-narrative-layer-01KQ8TD1 WP10
		StrategySummary,
		StrategyDropOldest,
		StrategySemanticCluster,
		StrategyCustomSubgraph,
	}
}

// ContextSlice is the input to a compactor. It carries the messages
// the kernel wants compacted plus enough context for the strategy to
// make a decision (target token budget, originating site, etc.).
type ContextSlice struct {
	// Messages is the chronologically-ordered slice the strategy will
	// shrink.
	Messages []agentgraph.Message
	// SystemPrompt, when non-empty, is preserved across compaction —
	// strategies must include it untouched in their output messages.
	SystemPrompt string
	// TargetTokens is the upper bound the strategy must aim to land
	// under. Zero means "no specific target — apply default".
	TargetTokens int
	// CurrentTokens is the caller's estimate of the input size. Used
	// for telemetry (bytes_saved / tokens_saved on the emitted event)
	// and, with ContextWindow, to evaluate PreCallThreshold.
	CurrentTokens int
	// ContextWindow is the active model's maximum context length in
	// tokens, 0 when unknown. Pipeline.Run needs it to decide whether a
	// call is actually over budget: without it there is no denominator
	// for SiteConfig.PreCallThreshold, so the automatic sites skip
	// rather than fire blind.
	ContextWindow int
	// Site identifies which invocation site triggered this compaction.
	// Strategies may use it to make different choices (e.g. post-tool
	// trim only operates on the latest message).
	Site Site
}

// CompactedContext is the result of a compaction run.
type CompactedContext struct {
	// Messages is the compacted slice the kernel will use in place of
	// the input messages.
	Messages []agentgraph.Message
	// TokensAfter is the strategy's estimate of the new size.
	TokensAfter int
	// Strategy records which strategy actually ran (useful when the
	// pipeline picks one based on cascading config).
	Strategy Strategy
	// BytesSaved is the total byte delta between input and output
	// message contents (UTF-8 bytes; not exact tokens but a useful
	// telemetry signal).
	BytesSaved int
}

// CompactOpts narrows a single compaction request.
type CompactOpts struct {
	// Strategy forces a specific strategy. Empty means "use the
	// pipeline's resolved strategy".
	Strategy Strategy
	// MaxRecursionDepth bounds nested custom_subgraph compactions.
	// Zero means "use the default of 2" (FR-045).
	MaxRecursionDepth int
	// CustomGraph is the agentgraph.Graph used by StrategyCustomSubgraph.
	// Ignored for other strategies.
	CustomGraph *agentgraph.Graph
	// SubgraphInputPort + SubgraphOutputPort name the ports on the
	// custom graph that carry the messages slice. Defaults are
	// "messages" / "messages".
	SubgraphInputPort  string
	SubgraphOutputPort string
	// SummaryProvider + SummaryModel select the model for
	// StrategySummary. Empty falls back to the pipeline default.
	SummaryProvider string
	SummaryModel    string
	// DropOldestKeepRecentN is the number of recent messages to keep
	// untouched when StrategyDropOldest fires. Zero means "use default".
	DropOldestKeepRecentN int
	// SemanticClusterCount is the desired cluster count for
	// StrategySemanticCluster. Zero means "use default".
	SemanticClusterCount int
	// Now overrides the clock. Tests use this to make event timestamps
	// deterministic.
	Now func() time.Time
}

// Compactor is the strategy interface every algorithm satisfies. The
// pipeline composes a strategy stack and dispatches based on the
// resolved CompactOpts.
type Compactor interface {
	Strategy() Strategy
	Compact(ctx context.Context, in ContextSlice, opts CompactOpts) (CompactedContext, error)
}

// KernelRunner is the seam custom_subgraph uses to recursively invoke
// the kernel on a user-supplied Graph. Production wiring binds this
// to the real Kernel.Run; tests pass a fake.
//
// Defining the interface here (rather than importing kernel directly)
// keeps the compaction package free of cycle risk. The kernel
// constructor accepts a Compactor interface (defined in
// core/agentgraph) and the kernel itself implements KernelRunner.
type KernelRunner interface {
	// RunGraph fires `graph` once with `inputs` keyed by entrypoint
	// node ID. Returns the leaf outputs keyed by terminal node ID.
	RunGraph(
		ctx context.Context,
		graph *agentgraph.Graph,
		inputs map[string]agentgraph.PortValues,
	) (map[string]agentgraph.PortValues, error)
}

// EventEmitter is the seam the pipeline uses to record compaction
// events. The kernel passes its EventLog-backed emitter; tests pass a
// recording fake.
type EventEmitter interface {
	Emit(runID, nodeID string, payload Event) error
}

// Event is the structured payload for the EventCompactionFired event
// kind. The pipeline assembles one of these on every fire (success
// path, skip path, recursion-cap path) and hands it to the emitter.
type Event struct {
	Strategy   Strategy `json:"strategy"`
	Site       Site     `json:"site"`
	BytesSaved int      `json:"bytes_saved,omitempty"`
	BytesIn    int      `json:"bytes_in,omitempty"`
	BytesOut   int      `json:"bytes_out,omitempty"`
	MsgsIn     int      `json:"messages_in,omitempty"`
	MsgsOut    int      `json:"messages_out,omitempty"`
	Skipped    bool     `json:"skipped,omitempty"`
	SkipReason string   `json:"skip_reason,omitempty"`
}

// Sentinels surfaced by the pipeline + strategies. Wrap with %w to
// preserve identity through callers.
var (
	// ErrUnknownStrategy reports a strategy name that is not one of
	// the four supported algorithms.
	ErrUnknownStrategy = errors.New("compaction: unknown strategy")
	// ErrCustomSubgraphMissing reports an attempt to run
	// StrategyCustomSubgraph without a CustomGraph in CompactOpts.
	ErrCustomSubgraphMissing = errors.New("compaction: custom subgraph not provided")
	// ErrRecursionExceeded fires when a custom_subgraph itself triggers
	// compaction past the depth bound (FR-045).
	ErrRecursionExceeded = errors.New("compaction: recursion depth exceeded")
	// ErrNoLLMSummarizer reports that StrategySummary needs an LLM
	// provider but none was wired into the strategy.
	ErrNoLLMSummarizer = errors.New("compaction: no llm summarizer configured")
	// ErrNoEmbedder reports that StrategySemanticCluster needs an
	// embedder but none was wired in.
	ErrNoEmbedder = errors.New("compaction: no embedder configured")
	// ErrNoKernelRunner reports that StrategyCustomSubgraph needs a
	// kernel runner but none was wired in.
	ErrNoKernelRunner = errors.New("compaction: no kernel runner configured")
)

// DefaultMaxRecursionDepth is the depth bound applied to
// custom_subgraph compaction when CompactOpts.MaxRecursionDepth is 0.
// Per FR-045 / spec: 2.
const DefaultMaxRecursionDepth = 2

// recursionKey is the context-key used by the pipeline to track
// in-progress custom_subgraph nesting. It is unexported and uses a
// distinct type to avoid collision with other context-keyed values.
type recursionKey struct{}

// withDepth returns a derived context whose recursion-depth counter is
// incremented by one. depth(ctx) reads the current value back.
func withDepth(ctx context.Context, n int) context.Context {
	return context.WithValue(ctx, recursionKey{}, n)
}

// depth reads the current recursion-depth counter, defaulting to 0.
func depth(ctx context.Context) int {
	v, _ := ctx.Value(recursionKey{}).(int)
	return v
}

// bytesOf totals the UTF-8 byte length of every message's content +
// name. Cheap and deterministic.
func bytesOf(ms []agentgraph.Message) int {
	n := 0
	for _, m := range ms {
		n += len(m.Content) + len(m.Name)
	}
	return n
}
