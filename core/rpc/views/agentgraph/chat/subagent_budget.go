package chat

import "sync"

// SubagentBudget carries a sub-agent profile's explicit BudgetTokens /
// BudgetTimeS (core/agents.Profile) from spawn time through to
// StartStream's env.Budget construction.
//
// Zero fields mean "the profile did not declare one" — same convention
// coreag.Budget itself uses (checkBudget's `> 0` guards).
type SubagentBudget struct {
	Tokens        int
	WallclockSecs int
}

// SubagentBudgetRegistry is a small session-keyed side channel between
// core/rpc.NewSubagentRunSpawner (the writer: one Set per spawned
// branch, right before it calls the SAME StartStream the interactive
// chat surface uses) and ChatRunner.StartStream (the reader: consulted
// once per turn on the spawned child session).
//
// Why a side channel rather than a StartStream parameter: StartStream
// is the method backing the Wails-bound LLM_StartStream RPC
// (frontend/wailsjs/go/rpc/Bindings.d.ts) — its signature is frontend
// contract, not free to change for a backend-only feature. Every other
// per-run knob this runner resolves (autonomy tier, reasoning budget,
// compaction watermark) uses the same shape: a Config-level resolver
// keyed by session/ctx rather than an argument.
//
// Filed alongside the tier-driven budget-cap work (owner directive
// 2026-09-09, mission requirement 3: "keep the sub-agent profile's
// explicit BudgetTokens/BudgetTimeS working"). See
// applyProfileBudgetClamp in chat_runner.go for how a recorded value
// combines with the tier-derived ceiling once read — clamp, not
// override: an explicit profile budget can only narrow the tier's
// ceiling, never widen it past what the dispatching session's autonomy
// tier allows.
//
// Entries persist for the session's lifetime rather than being
// consumed on first read: a spawned worker's declared budget governs
// every turn it takes, not only its first. NewSubagentRunSpawner clears
// the entry once the spawned run reaches a terminal state so the map
// does not grow unbounded across a long-lived harness process.
type SubagentBudgetRegistry struct {
	mu      sync.Mutex
	entries map[string]SubagentBudget
}

// NewSubagentBudgetRegistry constructs an empty registry.
func NewSubagentBudgetRegistry() *SubagentBudgetRegistry {
	return &SubagentBudgetRegistry{entries: make(map[string]SubagentBudget)}
}

// Set records the profile-declared budget for a spawned child session.
// A nil receiver or empty sessionID is a no-op — callers do not need to
// guard the call site themselves.
func (r *SubagentBudgetRegistry) Set(sessionID string, b SubagentBudget) {
	if r == nil || sessionID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[sessionID] = b
}

// Get returns the recorded budget for sessionID, if any. A nil receiver
// reports not-found, matching every other nil-safe resolver in this
// package (e.g. ChatRunner.autonomyKnobs on a nil AutonomyKnobs
// provider).
func (r *SubagentBudgetRegistry) Get(sessionID string) (SubagentBudget, bool) {
	if r == nil {
		return SubagentBudget{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.entries[sessionID]
	return b, ok
}

// Clear removes the recorded budget for sessionID. Called once the
// spawned run reaches a terminal state (core/rpc's awaitSubagentRun) so
// a long harness session's history of sub-agent dispatches does not
// leak entries forever.
func (r *SubagentBudgetRegistry) Clear(sessionID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.entries, sessionID)
}
