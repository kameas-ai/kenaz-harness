package compaction

import (
	"context"
	"fmt"
	"log"

	"github.com/sigil-tech/kaneaz-harness/core/agentgraph"
)

// StrategyNarrativeFirst is the narrative-layer compaction strategy
// (memory-narrative-layer-01KQ8TD1 WP10). It splices per-turn synthesised
// narratives verbatim for message ranges that have coverage. Ranges without
// narrative coverage emit a "pass-through" sentinel and the pipeline falls
// through to the next cascade tier (typically StrategySummary).
//
// This strategy MUST NOT be invoked at SiteManual — manual compact
// preserves "force fresh summary" UX. The pipeline enforces this via the
// site-disabled check in Run when SiteManual is configured to exclude
// narrative_first.
const StrategyNarrativeFirst Strategy = "narrative_first"

// NarrativeLookup is the narrow interface NarrativeFirstStrategy uses to
// retrieve synthesised narrative chunks for a message range. The production
// wiring binds this to core/memory.Store + a session-scoped filter; tests
// pass a fake.
//
// For a given (sessionID, turnIDs) slice, it returns the narrative content
// string or empty string when no synthesised narrative covers that range.
type NarrativeLookup interface {
	// FindNarrative returns the synthesised narrative content for the
	// given sessionID and set of turnIDs. Returns ("", nil) when no
	// narrative exists (not an error — the caller falls through to the
	// next cascade tier).
	FindNarrative(ctx context.Context, sessionID string, turnIDs []string) (content string, err error)
}

// NarrativeFirstStrategy implements the narrative_first compaction
// algorithm. When a narrative covers the entire compaction range, it
// replaces all messages in that range with a single system-tagged
// narrative message. When no narrative is found the strategy returns
// the input messages unchanged (pass-through).
//
// Shadow mode (HARNESS_MEMORY_NARRATIVE_COMPACT_SHADOW=true) runs both
// this strategy and the fallback, logs divergence, but uses only this
// strategy's output.
type NarrativeFirstStrategy struct {
	lookup    NarrativeLookup
	sessionID string
}

// NewNarrativeFirstStrategy constructs a NarrativeFirstStrategy.
// sessionID is the active session — used to key narrative lookups.
// lookup may be nil; in that case every call is a pass-through.
func NewNarrativeFirstStrategy(sessionID string, lookup NarrativeLookup) *NarrativeFirstStrategy {
	return &NarrativeFirstStrategy{
		sessionID: sessionID,
		lookup:    lookup,
	}
}

// Strategy returns the strategy identifier.
func (s *NarrativeFirstStrategy) Strategy() Strategy { return StrategyNarrativeFirst }

// Compact runs the narrative_first algorithm.
//
// Algorithm:
//  1. Call FindNarrative for the session with the count of messages as
//     context (the lookup decides whether narratives cover the range).
//  2. If a narrative is found → return it as a single system-tagged message.
//  3. If no narrative → return input unchanged (pass-through sentinel).
func (s *NarrativeFirstStrategy) Compact(ctx context.Context, in ContextSlice, _ CompactOpts) (CompactedContext, error) {
	// Pass-through when disabled or no lookup wired.
	if s.lookup == nil || len(in.Messages) == 0 {
		return passThrough(in), nil
	}

	// We pass nil turn IDs since the lookup can decide based on session.
	narrativeContent, err := s.lookup.FindNarrative(ctx, s.sessionID, nil)
	if err != nil {
		// Log but don't fail — fall through to the next tier.
		log.Printf("compaction: narrative_first lookup error: %v", err)
		return passThrough(in), nil
	}
	if narrativeContent == "" {
		// No narrative coverage — pass-through so the pipeline cascades.
		return passThrough(in), nil
	}

	// Build the compacted output: system prompt (if any) + single narrative message.
	compacted := make([]agentgraph.Message, 0, 2)
	if in.SystemPrompt != "" {
		compacted = append(compacted, agentgraph.Message{
			Role:    "system",
			Content: in.SystemPrompt,
		})
	}
	compacted = append(compacted, agentgraph.Message{
		Role:    "system",
		Content: fmt.Sprintf("[narrative]\n%s", narrativeContent),
	})

	bytesIn := bytesOf(in.Messages)
	bytesOut := bytesOf(compacted)

	return CompactedContext{
		Messages:    compacted,
		TokensAfter: approxTokens(bytesOut),
		Strategy:    StrategyNarrativeFirst,
		BytesSaved:  bytesIn - bytesOut,
	}, nil
}

// passThrough returns the input messages unchanged with Strategy set to
// StrategyNarrativeFirst and BytesSaved=0. The pipeline recognises
// BytesSaved=0 + pass-through content as "no-op from this strategy".
func passThrough(in ContextSlice) CompactedContext {
	msgs := append([]agentgraph.Message(nil), in.Messages...)
	return CompactedContext{
		Messages:    msgs,
		TokensAfter: approxTokens(bytesOf(msgs)),
		Strategy:    StrategyNarrativeFirst,
		BytesSaved:  0,
	}
}
