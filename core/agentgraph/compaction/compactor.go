// Package compaction is the harness's compaction subsystem — all of it.
// There is exactly one compaction package, and this is it.
//
// # Two layers, one package
//
// Compaction happens at two altitudes, and this package holds both.
// They are layers of one subsystem, not two subsystems:
//
//   - The IN-MEMORY STRATEGY LAYER (FR-041..FR-045; mission
//     agent-kernel-graph Bundle D). Files: compactor.go, config.go,
//     pipeline.go, presets.go, strategies.go, strategy_*.go,
//     yaml_resolver.go. This is the kernel-managed mechanism that
//     shrinks the ContextGraph or message-history slice when budget
//     pressure rises. It runs at three invocation sites — token-budget
//     pre-call, post-tool result trim, and manual user trigger — and
//     dispatches to a Strategy. Nothing here touches storage; it
//     transforms a slice of messages and hands it back.
//
//   - The PERSISTED-HISTORY LAYER. Files: session_*.go, plus the
//     wiring/ subpackage. This is the summarize-then-replace engine
//     that rewrites a session's stored transcript: it folds the oldest
//     messages into a summary row, flips the originals to compacted,
//     runs the rolling-summary mode for the "maximal" tier, and
//     soft-archives on a schedule. It reaches real storage, which is
//     what the wiring/ subpackage exists to adapt.
//
// The session layer is reached FROM the strategy layer, not around it:
// StrategySessionRewrite (core/rpc/views/agentgraph/chat) dispatches
// through Pipeline at SiteManual and drives SessionEngine underneath.
// One entry point, two altitudes.
//
// # Why they are one package now
//
// They used to be two: this package, and a second `core/compaction`
// that predated the kernel. That was the defect
// agentgraph-total-convergence-01PMGX01 exists to close — the harness
// shipped two compaction systems, one of which was configured and
// unreachable, with a boolean on the Env whose only job was stopping
// both from firing on the same turn. WP08 made the dial an ordinary
// strategy behind the one pipeline; WP10a (this merge) folded the
// remaining persisted-history code in here so the package boundary
// tells the truth about the architecture.
//
// The session-layer symbols carry a Session/Sweep prefix
// (SessionEngine, SessionMessage, SweepScheduler) so a reader can tell
// the two layers apart at a call site without checking the file. The
// filename prefix `session_` does the same job at the directory level.
//
// # Dependency direction
//
// The strategy layer depends on `core/agentgraph` for value types
// (Message, LLMRequest, Graph) and may depend on `core/llm` for the
// summary strategy and `core/corpus` for the embedding seam used by
// `semantic_cluster`. Crucially the dependency direction is one-way:
// `core/agentgraph` does NOT import this package — instead it consumes
// the `agentgraph.Compactor` interface this package satisfies.
//
// The five-tier dial's numerics are NOT here. They live in
// `core/compactionpolicy`, a leaf package with no imports, because
// `core/llm` also has to name a tier and importing this package from
// there would close a cycle (core/llm -> compaction -> core/agentgraph
// -> core/llm). See that package's doc for the full reasoning.
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
	// SitePostTool fires after a ToolNode returns, gated by the
	// pre_call site's token-watermark verdict, NOT by result byte size
	// against tool_result_max_bytes — see SiteConfig.ToolResultMaxBytes's
	// doc comment (config.go) for the CK-04/CK-05/CK-06 justify() block.
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
	// StrategySessionRewrite rewrites the *persisted* session history
	// rather than an in-memory slice: it summarises the oldest span of
	// the conversation into a stored summary row and soft-archives the
	// originals, so the shrink survives the turn.
	//
	// This is the strategy the chat surface's five-tier aggressiveness
	// dial (off / conservative / balanced / aggressive / maximal)
	// resolves to. Before
	// agentgraph-total-convergence-01PMGX01 WP08 that dial ran as a
	// pre-kernel pass on the chat surface that this package knew
	// nothing about, which is what made the harness ship two
	// compaction systems. It is now an ordinary strategy behind the
	// ordinary pipeline, reached from the ordinary `compact` node.
	//
	// Unlike the other strategies it needs run identity (which session,
	// which model) rather than just a message slice, so its
	// implementation is bound per-run via Pipeline.Bind rather than
	// registered once at construction.
	StrategySessionRewrite Strategy = "session_rewrite"
)

// AllStrategies enumerates the supported strategies.
func AllStrategies() []Strategy {
	return []Strategy{
		StrategyNarrativeFirst, // memory-narrative-layer-01KQ8TD1 WP10
		StrategySummary,
		StrategyDropOldest,
		StrategySemanticCluster,
		StrategyCustomSubgraph,
		StrategySessionRewrite, // agentgraph-total-convergence-01PMGX01 WP08
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
	// SessionID is the conversation this compaction belongs to, copied
	// from the request scope by Pipeline.Run.
	//
	// Most strategies ignore it: they receive the slice they are asked
	// to shrink and hand a smaller one back, and where those messages
	// came from is not their business. StrategySessionRewrite is the
	// exception — its whole job is to rewrite persisted history, which
	// it cannot address without knowing which session. Empty means "no
	// session scope", and a session-scoped strategy must skip rather
	// than guess.
	SessionID string
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
	// Now overrides the clock. Assigned from Pipeline.now and cloned
	// through Bind(), but never called — see WithClock's doc comment
	// (pipeline.go) for the CK-04/CK-05/CK-06 justify() block: Event
	// has no timestamp field for it to stamp.
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
