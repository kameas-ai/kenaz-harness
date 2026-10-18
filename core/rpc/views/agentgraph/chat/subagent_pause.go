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
//
// PR #334 review fixed a registry-entry leak (a paused-then-aborted
// run left its entry forever, since nothing but an explicit Resume
// released it) by having driveRun's cleanup defer call Resume(sessionID)
// unconditionally on every exit. That is unsound on its own: sessionIDs
// are genuinely reused across sequential runs on the same session
// (ChatRunner.RedriveLastTurn re-issues StartStream for the same
// session; so does every ordinary next chat turn), so an
// unconditional, session-keyed Resume from a *stale* run's cleanup can
// silently clear a *different, newer* run's freshly-armed pause —
// dropping a user's pause with no error and no trace. BeginRun/EndRun
// below close that hole: each run claims a generation token at start
// and EndRun only ever releases the entry if it is still owned by that
// same generation, so a run's own cleanup can never clear a pause a
// later run on the same sessionID has since armed for itself. Resume
// (the RPC-facing, explicit-user-resume path) is deliberately left
// generation-agnostic — an explicit ResumeSubagent call always clears
// whatever is currently armed for the session, no ownership check,
// because there is no ambiguity to resolve: the user is resuming
// "whatever is paused right now" by definition.
type SubagentPauseRegistry struct {
	mu sync.Mutex
	// paused is sessionID -> the armed entry; entry present == paused.
	paused map[string]*pauseEntry
	// activeGen is sessionID -> the generation BeginRun most recently
	// handed out for that session and which EndRun has not yet retired.
	// Used only to attribute a newly-armed Pause entry to "whichever run
	// is currently live for this session" — Wait/IsPaused never consult
	// it, only Pause (to stamp ownership) and BeginRun (to claim an
	// already-armed, unowned entry).
	activeGen map[string]uint64
	// nextGen is a process-wide monotonic counter; 0 is reserved to mean
	// "no run has claimed this entry yet" (an entry armed by Pause
	// before any BeginRun call for that session, or after the owning
	// run's EndRun already ran), so real generations start at 1.
	nextGen uint64
}

// pauseEntry is one session's armed pause signal plus the generation
// token (see BeginRun/EndRun) of the run allowed to release it via
// EndRun. owner == 0 means "unclaimed" — either no run has started for
// this session since Pause armed it, or the owning run has already
// ended. An unclaimed entry is only ever cleared by an explicit Resume
// call or claimed by the next BeginRun for the same session; see
// EndRun's doc for why that is a bounded, accepted gap rather than a
// reintroduction of the original leak.
type pauseEntry struct {
	ch    chan struct{}
	owner uint64
}

// NewSubagentPauseRegistry constructs an empty registry.
func NewSubagentPauseRegistry() *SubagentPauseRegistry {
	return &SubagentPauseRegistry{
		paused:    make(map[string]*pauseEntry),
		activeGen: make(map[string]uint64),
	}
}

// Pause arms the pause signal for sessionID. Returns true if this call
// actually changed the state (the session was not already paused) —
// the caller's idempotency signal: core/rpc/views/branches.API.
// PauseSubagent audits only when changed is true, mirroring PR #331's
// Abort idempotency contract ("one audit record, not two"). A nil
// receiver or empty sessionID is a no-op that reports changed=false —
// callers do not need to guard the call site themselves.
//
// The new entry is stamped with sessionID's current activeGen (0 if no
// run has called BeginRun for this session, or the owning run already
// called EndRun) — this is what lets EndRun tell "my run's pause" from
// "a different run's pause" apart later.
func (r *SubagentPauseRegistry) Pause(sessionID string) (changed bool) {
	if r == nil || sessionID == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.paused[sessionID]; ok {
		return false
	}
	r.paused[sessionID] = &pauseEntry{
		ch:    make(chan struct{}),
		owner: r.activeGen[sessionID],
	}
	return true
}

// Resume clears the pause signal for sessionID, unblocking any Wait
// call currently parked on it so the run's loop proceeds to its next
// turn. Returns true if this call actually changed the state (the
// session was paused) — same idempotency-signal contract as Pause. A
// nil receiver or empty sessionID is a no-op that reports
// changed=false.
//
// Deliberately unconditional / generation-agnostic — this is the
// RPC-facing ResumeSubagent path, an explicit user action with no
// ownership ambiguity to resolve. EndRun below is the generation-aware
// sibling driveRun's cleanup defer uses instead of this method.
func (r *SubagentPauseRegistry) Resume(sessionID string) (changed bool) {
	if r == nil || sessionID == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.paused[sessionID]
	if !ok {
		return false
	}
	close(entry.ch)
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
// No polling: each paused entry owns a channel that Resume/EndRun
// closes, so a blocked Wait wakes the instant the session is released
// rather than on the next poll tick.
func (r *SubagentPauseRegistry) Wait(ctx context.Context, sessionID string) error {
	if r == nil {
		return nil
	}
	for {
		r.mu.Lock()
		entry, ok := r.paused[sessionID]
		r.mu.Unlock()
		if !ok {
			return nil
		}
		select {
		case <-entry.ch:
			// Unpaused — but re-check rather than assuming: a second
			// Pause could have raced in between the close and this
			// goroutine waking (a fresh entry would already be
			// installed under a new Pause call). The loop above handles
			// that by re-reading r.paused[sessionID] on the next pass.
			continue
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// BeginRun registers sessionID as having a new in-flight run and
// returns a generation token identifying it. driveRun calls this once,
// synchronously, inside StartStream (before the run's goroutine is
// even spawned) and carries the returned generation on its chatSub for
// EndRun to consume in the cleanup defer.
//
// If a pause entry is already armed for sessionID with no owner
// (owner == 0 — e.g. PauseSubagent fired before this run's StartStream
// call ever happened, the shape
// TestChatRunner_DriveRun_ReleasesPauseEntryOnAbort exercises),
// BeginRun claims it for this generation, so this run's own EndRun is
// what will release it. An entry that is already owned by a different,
// not-yet-ended run's generation is left untouched — that entry
// belongs to that other run and only that run's EndRun may release it.
//
// A nil receiver or empty sessionID returns 0 (the reserved "no run"
// generation) rather than panicking, matching every other method's
// nil-receiver-safety contract.
func (r *SubagentPauseRegistry) BeginRun(sessionID string) (generation uint64) {
	if r == nil || sessionID == "" {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextGen++
	gen := r.nextGen
	r.activeGen[sessionID] = gen
	if entry, ok := r.paused[sessionID]; ok && entry.owner == 0 {
		entry.owner = gen
	}
	return gen
}

// EndRun is BeginRun's release-side counterpart, called from driveRun's
// cleanup defer with the generation BeginRun returned for that run.
// This is what makes the cleanup defer's unconditional call (on every
// exit path — completed, aborted, panicked) safe to keep unconditional
// without reintroducing the run-scoped-release race a reviewer caught
// in PR #334: it releases sessionID's pause entry ONLY IF that entry is
// still owned by generation. A newer run on the same, reused sessionID
// has by then already called its own BeginRun, which either claimed a
// then-unowned entry for itself or — the case this exists for — a
// fresh Pause call armed after the newer run started was stamped with
// the newer run's own generation directly (Pause reads activeGen at
// arm time). Either way, generation no longer matches, so this is a
// no-op and the newer run's pause survives.
//
// generation == 0 is always a no-op — BeginRun's counter starts at 1,
// so 0 can never be a real owner; this covers both "this run never
// called BeginRun" (defensive) and lets callers pass a zero-value
// generation (e.g. a nil registry's BeginRun result) without a guard.
//
// activeGen[sessionID] is cleared only if it still equals generation —
// a superseded generation's EndRun must not erase a newer run's
// "a run is live for this session" marker.
//
// Known accepted gap: a Pause call that lands while no run is active
// for sessionID (activeGen has no entry — either no run has started
// yet for this session, or the previously-active run's EndRun already
// retired it) arms an owner==0 entry. If no later run ever calls
// BeginRun for that exact sessionID again, that entry is only ever
// cleared by an explicit Resume — it is not touched by any EndRun call
// (0 never matches a real generation). This differs from the original
// leak in kind and severity: it requires an explicit external Pause
// call racing a run boundary (not "every abort," which was the
// original bug's trigger), is bounded to one stale map entry per
// occurrence, and self-heals the instant either (a) a new run starts
// for that session (BeginRun claims it, so it is released by that
// run's own EndRun in the ordinary course of things) or (b) an
// explicit ResumeSubagent call arrives. No mission currently tracks
// closing this residual gap further (e.g. via a TTL); flagged here for
// the next owner who touches this registry.
func (r *SubagentPauseRegistry) EndRun(sessionID string, generation uint64) {
	if r == nil || sessionID == "" || generation == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.activeGen[sessionID] == generation {
		delete(r.activeGen, sessionID)
	}
	if entry, ok := r.paused[sessionID]; ok && entry.owner == generation {
		close(entry.ch)
		delete(r.paused, sessionID)
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
