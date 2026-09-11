package chat

import (
	"context"
	"sync"
)

// SubagentPauseRegistry is a small session-keyed side channel between
// core/rpc/views/branches.API.PauseSubagent/ResumeSubagent (the writer:
// Pause/Resume, called from the Subagent_Pause/Subagent_Resume RPCs)
// and the coreag.TurnPauseGate ChatRunner.StartStream wires onto every
// Env it builds (the reader: consulted by loopExecutor.Execute at the
// top of every Loop-node iteration — see core/agentgraph/exec_control.go
// and core/agentgraph/executor.go's Env.TurnPause doc).
//
// Mirrors SubagentBudgetRegistry's shape deliberately (same file
// neighbourhood, same session-keyed side-channel pattern) — see that
// type's doc for why a side channel rather than a StartStream
// parameter: StartStream's signature is frontend contract.
//
// subagent-control-and-background-tasks-01PMZB11 UNIT-8, owner ruling
// E-002: pause means "stop after the current turn, start no further
// turn"; resume clears that signal so turns begin again. This registry
// is the storage half; core/agentgraph's loop executor is the
// consumption half — CLAUDE.md's UNIT-8 note is explicit that the
// storage half alone (a status field nothing branches on) is the
// forbidden "manufactured success" shape. An entry existing here has no
// observable effect until Wait is actually called from the live run
// loop.
type SubagentPauseRegistry struct {
	mu     sync.Mutex
	paused map[string]chan struct{} // sessionID -> resume signal; entry present == paused
}

// NewSubagentPauseRegistry constructs an empty registry.
func NewSubagentPauseRegistry() *SubagentPauseRegistry {
	return &SubagentPauseRegistry{paused: make(map[string]chan struct{})}
}

// Pause arms the pause signal for sessionID. Returns true if this call
// actually changed the state (the session was not already paused) —
// the caller's idempotency signal: core/rpc/views/branches.API.
// PauseSubagent audits only when changed is true, mirroring PR #331's
// Abort idempotency contract ("one audit record, not two"). A nil
// receiver or empty sessionID is a no-op that reports changed=false —
// callers do not need to guard the call site themselves.
func (r *SubagentPauseRegistry) Pause(sessionID string) (changed bool) {
	if r == nil || sessionID == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.paused[sessionID]; ok {
		return false
	}
	r.paused[sessionID] = make(chan struct{})
	return true
}

// Resume clears the pause signal for sessionID, unblocking any Wait
// call currently parked on it so the run's loop proceeds to its next
// turn. Returns true if this call actually changed the state (the
// session was paused) — same idempotency-signal contract as Pause. A
// nil receiver or empty sessionID is a no-op that reports
// changed=false.
func (r *SubagentPauseRegistry) Resume(sessionID string) (changed bool) {
	if r == nil || sessionID == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ch, ok := r.paused[sessionID]
	if !ok {
		return false
	}
	close(ch)
	delete(r.paused, sessionID)
	return true
}

// IsPaused reports whether sessionID currently has an armed pause
// signal. A nil receiver reports false.
func (r *SubagentPauseRegistry) IsPaused(sessionID string) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.paused[sessionID]
	return ok
}

// Wait blocks while sessionID is paused. It returns nil once unpaused
// (including immediately, if sessionID was never paused or the
// registry is nil), or ctx.Err() if ctx is cancelled/expired first —
// the path Subagent_Abort's ctx cancellation uses to unblock a paused
// run rather than leaving it parked forever (mid-turn cost is not what
// Pause bounds; a paused-and-then-aborted run still stops for real).
//
// No polling: each paused entry owns a channel that Resume closes, so
// a blocked Wait wakes the instant Resume is called rather than on the
// next poll tick.
func (r *SubagentPauseRegistry) Wait(ctx context.Context, sessionID string) error {
	if r == nil {
		return nil
	}
	for {
		r.mu.Lock()
		ch, ok := r.paused[sessionID]
		r.mu.Unlock()
		if !ok {
			return nil
		}
		select {
		case <-ch:
			// Unpaused — but re-check rather than assuming: a second
			// Pause could have raced in between the close and this
			// goroutine waking (a fresh channel would already be
			// installed under a new Pause call). The loop above handles
			// that by re-reading r.paused[sessionID] on the next pass.
			continue
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// sessionTurnPauseGate adapts SubagentPauseRegistry (session-keyed) to
// coreag.TurnPauseGate (no-argument Wait(ctx) — Env is already scoped
// to one session). ChatRunner.StartStream installs one of these on
// every Env it builds; a nil reg makes Wait always return nil
// immediately (SubagentPauseRegistry.Wait is nil-receiver-safe), so
// this is safe to wire unconditionally rather than only for sessions
// known to be sub-agent children.
type sessionTurnPauseGate struct {
	reg       *SubagentPauseRegistry
	sessionID string
}

func (g sessionTurnPauseGate) Wait(ctx context.Context) error {
	return g.reg.Wait(ctx, g.sessionID)
}
