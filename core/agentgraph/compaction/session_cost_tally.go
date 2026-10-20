package compaction

import (
	"context"
	"sync"
)

// CallCostTally collects the cost of the summarisation calls made under
// one context, so a caller that triggers compaction can attribute what it
// cost to the work that triggered it. The process-wide running total
// (wiring.LLMCaller.Overhead) cannot answer that: it is one counter for
// every compaction since boot.
//
// The LLMCaller adapter (core/agentgraph/compaction/wiring) adds every
// completed CallForSummary to the tally found on its ctx; the engine
// itself never touches it. First caller: the scheduled-chat dispatcher,
// which reports a fire's summarisation cost on that fire's history row
// (scheduled-chat-context-budget).
type CallCostTally struct {
	mu            sync.Mutex
	calls         int
	totalUSD      float64
	indeterminate int
	inputTokens   int
	outputTokens  int
}

// CallCostSnapshot is a read of a CallCostTally.
type CallCostSnapshot struct {
	// Calls is every completed summarisation call.
	Calls int
	// TotalUSD sums the calls whose cost was determinate.
	TotalUSD float64
	// Indeterminate counts calls with no price for their model; their
	// cost is not in TotalUSD.
	Indeterminate int
	InputTokens   int
	OutputTokens  int
}

type callCostTallyKey struct{}

// WithCallCostTally returns ctx carrying a fresh tally, and the tally.
func WithCallCostTally(ctx context.Context) (context.Context, *CallCostTally) {
	t := &CallCostTally{}
	return context.WithValue(ctx, callCostTallyKey{}, t), t
}

// CallCostTallyFrom returns the tally on ctx, or nil.
func CallCostTallyFrom(ctx context.Context) *CallCostTally {
	if ctx == nil {
		return nil
	}
	t, _ := ctx.Value(callCostTallyKey{}).(*CallCostTally)
	return t
}

// Add records one completed call. A nil tally is a no-op.
func (t *CallCostTally) Add(totalUSD float64, indeterminate bool, inputTokens, outputTokens int) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.calls++
	t.inputTokens += inputTokens
	t.outputTokens += outputTokens
	if indeterminate {
		t.indeterminate++
		return
	}
	t.totalUSD += totalUSD
}

// Snapshot reads the tally. A nil tally reads as zero.
func (t *CallCostTally) Snapshot() CallCostSnapshot {
	if t == nil {
		return CallCostSnapshot{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return CallCostSnapshot{
		Calls:         t.calls,
		TotalUSD:      t.totalUSD,
		Indeterminate: t.indeterminate,
		InputTokens:   t.inputTokens,
		OutputTokens:  t.outputTokens,
	}
}
