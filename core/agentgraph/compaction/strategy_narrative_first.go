package compaction

import (
	"context"
	"fmt"
	"log"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph"
)

// StrategyNarrativeFirst is the narrative-layer compaction strategy
// (memory-narrative-layer-01KQ8TD1 WP10). It splices per-turn synthesised
// narratives verbatim for message ranges that have coverage. Ranges
// without narrative coverage return the input unchanged (pass-through,
// see passThrough's own doc for the CK-13 correction on what that
// actually causes downstream).
//
// CK-13 justify(blocker: "NewNarrativeFirstStrategy has zero production
// call sites — a repo-wide grep finds only its own declaration and
// test files — so the strategy is not registered on any pipeline at
// all; the enforcement + shadow-mode claims below described a
// mechanism for a strategy nothing dispatches", owner: alec, date:
// 2026-08-29; chat-turn-integrity-01PMZ606 WP13): this used to claim
// "MUST NOT be invoked at SiteManual... The pipeline enforces this via
// the site-disabled check in Run when SiteManual is configured to
// exclude narrative_first." SiteConfig has no per-strategy exclusion
// field at all (only a whole-site Enabled bool) — `grep -n "Exclude"
// core/agentgraph/compaction/config.go` is empty — so no such
// enforcement exists to configure. If this strategy is ever wired, the
// SiteManual exclusion needs building from scratch, not flipping on.
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
// CK-13 (chat-turn-integrity-01PMZ606 WP13): this used to describe a
// live shadow mode — "Shadow mode
// (HARNESS_MEMORY_NARRATIVE_COMPACT_SHADOW=true) runs both this
// strategy and the fallback, logs divergence, but uses only this
// strategy's output." core/memory/narrative.NarrativeCompactShadowMode
// reads that env var correctly, but has zero callers anywhere in this
// package (or the tree) — nothing runs a comparison, nothing logs a
// divergence. See StrategyNarrativeFirst's doc for the justify() block
// covering why: this strategy is not registered on any pipeline yet,
// so there is no "both" to run a shadow comparison between.
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
		// Log but don't fail — return the input unchanged (CK-13: this
		// is NOT a cascade to a fallback strategy; see passThrough's doc).
		log.Printf("compaction: narrative_first lookup error: %v", err)
		return passThrough(in), nil
	}
	if narrativeContent == "" {
		// No narrative coverage — pass-through (CK-13: not a cascade).
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
// StrategyNarrativeFirst and BytesSaved=0.
//
// CK-13 (chat-turn-integrity-01PMZ606 WP13): this used to claim "The
// pipeline recognises BytesSaved=0 + pass-through content as 'no-op
// from this strategy'" — implying a cascade to a fallback strategy.
// pipeline.go has no reference to StrategyNarrativeFirst, BytesSaved,
// or any per-strategy fallback dispatch; a repo-wide grep confirms it.
// A pass-through result is just this strategy's normal successful
// output (Strategy() still reports narrative_first, no error) — the
// pipeline has no mechanism to notice BytesSaved==0 and re-dispatch to
// a different strategy for the same request.
func passThrough(in ContextSlice) CompactedContext {
	msgs := append([]agentgraph.Message(nil), in.Messages...)
	return CompactedContext{
		Messages:    msgs,
		TokensAfter: approxTokens(bytesOf(msgs)),
		Strategy:    StrategyNarrativeFirst,
		BytesSaved:  0,
	}
}
