package compaction

import "fmt"

// This file is part of the PERSISTED-HISTORY layer of the compaction
// package — the half that rewrites a session's stored transcript,
// as opposed to the in-memory FR-041 strategy layer it now sits beside.
// It moved here from core/compaction in
// agentgraph-total-convergence-01PMGX01 WP10a, which merged the
// harness's two compaction packages into one: the two are layers of a
// single subsystem, not two subsystems. The `session_` filename prefix
// marks the layer at a glance; compactor.go carries the package-level
// map of how the layers fit together.
//
// ErrCompactionModelTooSmall is returned when the span proposed for
// compaction exceeds the chosen compaction model's MaxContextTokens
// (per its CapabilityDescriptor). Caller should either pick a larger
// compaction model or use a more conservative aggressiveness tier.
//
// The struct carries both numbers so the chat-runner / UI surface can
// render an actionable "your compaction model fits N but the span needs
// M" copy without re-deriving the values.
type ErrCompactionModelTooSmall struct {
	NeedsTokens    int
	ModelMaxTokens int
}

// Error renders the typed error for logs and bubbled-up provider error
// surfaces.
func (e *ErrCompactionModelTooSmall) Error() string {
	return fmt.Sprintf("compaction span requires %d tokens but compaction model caps at %d",
		e.NeedsTokens, e.ModelMaxTokens)
}

// ErrCompactionDuringToolPair is documented as SessionEngine.Compact's
// defensive return value for the case where boundary-snap fails to
// produce a tool-pair-safe boundary.
//
// CK-10 justify(blocker: "session_snap.go's snapBoundaryForToolPairs /
// snapBoundaryBackForToolPairs are unconditional int-returning
// functions with no failure mode to report — they snap to a safe
// boundary by construction (a bounded forward/backward search over a
// finite slice always terminates), so there is currently no call site
// that COULD return this sentinel, not merely one that forgot to",
// owner: alec, date: 2026-08-29; chat-turn-integrity-01PMZ606 WP13):
// returned by nothing and — contrary to its own "tests pin the
// contract" claim — asserted by no test either. Under A-0 this is not
// deleted. Wiring it for real would mean making boundary-snap
// fallible, which is a design change to session_snap.go, not a missing
// wire in session_engine.go.
var ErrCompactionDuringToolPair = fmt.Errorf("compaction boundary would split a tool_use/tool_result pair")

// ErrSessionFull is returned when a session has hit its provider's
// MaxContextTokens cap AND aggressiveness=off (or compaction itself
// can't fit). Caller surfaces an honest "session full" UX rather than
// silently truncating history (plan §2.3, §2.6).
//
// The compaction engine itself does not return ErrSessionFull directly
// — it's the chat runner's job to translate "compaction was a no-op
// AND we're still over cap" into this sentinel. The error is exported
// here because the engine package is the natural home for compaction's
// public sentinel surface.
var ErrSessionFull = fmt.Errorf("session has hit its context window and compaction is unavailable")
