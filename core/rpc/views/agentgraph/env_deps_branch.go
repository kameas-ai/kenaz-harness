package agentgraph

import (
	"context"
	"fmt"
	"sync"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	coreconv "github.com/kameas-ai/kenaz-harness/core/conversation"
	"github.com/kameas-ai/kenaz-harness/core/session"
)

// RunSpawner starts a headless child kernel run for a freshly forked
// sub-agent branch and returns a handle BranchSeamAdapter.WaitForChildRun
// can block on (subagent-control-and-background-tasks-01PMZB11 UNIT-6 —
// the "RunSpawner" hook this package's docs long promised but never
// built). Constructed in production by core/rpc's New()
// (core/rpc/subagent_run_spawner.go): it needs the LLM connector, the
// in-process event bus and the background-task registry, none of which
// this package may import (agentgraph views must not depend on
// core/rpc's chassis internals — the import would cycle back through
// core/rpc/views/agentgraph itself). Late-bound via SetRunSpawner
// rather than passed to NewBranchSeamAdapter because those dependencies
// don't exist yet at adapter-construction time: core/rpc/api.go builds
// the graph manager (and this adapter, as its Branch dep) BEFORE the
// LLM stack, because the LLM stack's ChatRunner needs the graph
// manager's GraphLoader.
//
// req is the SAME ForkRequest Fork received — branchID/childSessionID
// are Fork's own outputs, handed back here so the spawner doesn't have
// to re-derive them.
//
// nil (the zero value, and the adapter's default) preserves the
// pre-UNIT-6 behaviour: Fork does not spawn a run, and
// WaitForChildRun returns nil immediately without blocking on anything.
type RunSpawner func(ctx context.Context, branchID, childSessionID string, req coreag.ForkRequest) (SpawnedRun, error)

// MaxForkDepth bounds how many generations deep a chain of Fork calls
// may nest before Fork refuses to create another one. Named here — the
// ONE place — because nothing else in the spawn path bounds recursion:
// a sub-agent's child session is an ordinary Kind="chat" session
// (session.Manager.CreateInProject), kenaz__subagent_dispatch is
// always-on in the builtin predicate for every session including that
// one, and neither the tool nor the spawner carries a depth or
// generation counter of its own. Without this, a spawned sub-agent could
// dispatch a grandchild, which dispatches a great-grandchild, without
// limit (containment review of PR #307, finding N2). Root/interactive
// sessions are generation 0; a direct sub-agent is generation 1; Fork
// refuses to create generation MaxForkDepth+1.
//
// This bound is process-local, in-memory, and not persisted (see
// BranchSeamAdapter.generations) — a harness restart resets it. That is
// a real limitation, but it still closes the common-case hole (a single
// run recursing without limit inside one process) with a legible,
// single-file change, rather than leaving the mission's own
// depth/generation-field work (UNIT-9) as the only guard.
//
// This also bounds the pre-existing "edit and resend" fork path
// (CreationPath="edit_resend" below), since BranchSeamAdapter.Fork is
// the one seam both callers share — a deliberate, generous limit rather
// than a narrower model-only check, so one choke point protects both.
const MaxForkDepth = 3

// SpawnedRun is what a RunSpawner returns on success.
type SpawnedRun struct {
	// TaskID is the tasks.Registry id tracking this run, when a task
	// registry was wired into the spawner. Empty when none was — that's
	// a visibility loss only, not a correctness one (see Wait).
	TaskID string
	// Wait blocks until the spawned run reaches a terminal state, or
	// until ctx is cancelled — whichever comes first. nil is treated as
	// "already complete" (WaitForChildRun returns immediately).
	Wait func(ctx context.Context) error
}

// spawnOutcome is what Fork records for a branch once a RunSpawner has
// been invoked, for WaitForChildRun to pick back up.
type spawnOutcome struct {
	run SpawnedRun
	err error // non-nil when the spawner itself failed to start the run
}

// BranchSeamAdapter wraps core/conversation.Manager + session.Manager
// onto the agentgraph.BranchSeam interface. The kernel's ForkNode and
// MergeNode call into this for child-session lifecycle; production
// wiring binds the real managers here, tests bind the in-memory
// FakeBranchSeam.
//
// Fork itself only ever creates the child session and the branch row —
// that contract is unchanged by UNIT-6. What changed is what happens
// AFTER: when a RunSpawner is wired (SetRunSpawner), Fork also asks it
// to start a headless child kernel run and records the outcome for
// WaitForChildRun. With no spawner wired (the default), behaviour is
// byte-identical to pre-UNIT-6: no run is spawned, and
// WaitForChildRun is a no-op.
type BranchSeamAdapter struct {
	conversations *coreconv.Manager
	sessions      *session.Manager

	runSpawnerMu sync.RWMutex
	runSpawner   RunSpawner

	runsMu sync.Mutex
	runs   map[string]spawnOutcome // branchID -> outcome, populated by Fork, consumed (and evicted) by WaitForChildRun

	genMu       sync.Mutex
	generations map[string]int // sessionID -> fork generation, populated by Fork, read by Fork on the NEXT fork off that session. Never evicted (unlike runs): a child session must stay lookupable for as long as the process might see it dispatch again.
}

// NewBranchSeamAdapter constructs the adapter. Both managers may be nil
// — the resulting seam returns ErrNoBranchSeam from every method, which
// matches the kernel's nilBranchSeam stub. The defensive nil-check
// keeps the chassis bootable on the test path (rpc.New(nil)).
func NewBranchSeamAdapter(conversations *coreconv.Manager, sessions *session.Manager) *BranchSeamAdapter {
	return &BranchSeamAdapter{conversations: conversations, sessions: sessions}
}

// SetRunSpawner late-binds the child-run spawner after construction.
// core/rpc's New() calls this once the LLM connector, event bus and
// task registry all exist — which is AFTER this adapter is built (see
// the RunSpawner doc for why). Same late-binding shape as
// tasks.Registry.SetHookFirer (core/tasks/registry.go) for the
// identical reason: a boot-order dependency cycle, not a design
// preference. nil is safe — equivalent to never calling this (the
// pre-UNIT-6 no-run-spawned behaviour).
//
// Safe to call on a nil *BranchSeamAdapter (mirrors every other method
// here — see available()): a caller that hasn't checked whether a real
// adapter was constructed just gets a no-op instead of a panic.
func (a *BranchSeamAdapter) SetRunSpawner(spawner RunSpawner) {
	if a == nil {
		return
	}
	a.runSpawnerMu.Lock()
	defer a.runSpawnerMu.Unlock()
	a.runSpawner = spawner
}

func (a *BranchSeamAdapter) getRunSpawner() RunSpawner {
	if a == nil {
		return nil
	}
	a.runSpawnerMu.RLock()
	defer a.runSpawnerMu.RUnlock()
	return a.runSpawner
}

// available reports whether the adapter has been constructed with the
// minimal dependencies it needs to do real work.
func (a *BranchSeamAdapter) available() bool {
	return a != nil && a.conversations != nil && a.sessions != nil
}

// generationOf returns the recorded fork generation for sessionID, or 0
// when unrecorded — the correct default for a root/interactive session
// that has never itself been the child end of a Fork call.
func (a *BranchSeamAdapter) generationOf(sessionID string) int {
	a.genMu.Lock()
	defer a.genMu.Unlock()
	return a.generations[sessionID]
}

// setGeneration records gen as sessionID's fork generation.
func (a *BranchSeamAdapter) setGeneration(sessionID string, gen int) {
	a.genMu.Lock()
	defer a.genMu.Unlock()
	if a.generations == nil {
		a.generations = make(map[string]int)
	}
	a.generations[sessionID] = gen
}

// Fork allocates a new child session and persists a branch row.
// Writes the handoff prompt as the child's first user message. The
// kernel emits the branch_fork event on its side; this adapter only
// owns the storage transition.
func (a *BranchSeamAdapter) Fork(ctx context.Context, req coreag.ForkRequest) (coreag.BranchHandle, error) {
	if !a.available() {
		return coreag.BranchHandle{}, coreag.ErrNoBranchSeam
	}
	if req.ParentSessionID == "" {
		return coreag.BranchHandle{}, fmt.Errorf("branch_seam: parent_session_id required")
	}
	// N2 depth guard: refuse BEFORE creating anything, so a refusal never
	// leaves an orphaned branch/child session behind. See MaxForkDepth's
	// doc for why this lives here rather than in the model-facing tool.
	childGen := a.generationOf(req.ParentSessionID) + 1
	if childGen > MaxForkDepth {
		return coreag.BranchHandle{}, fmt.Errorf(
			"branch_seam: fork depth %d exceeds MaxForkDepth (%d) — a sub-agent may not spawn another sub-agent past this nesting level",
			childGen, MaxForkDepth)
	}
	br, child, err := a.conversations.CreateBranch(ctx, coreconv.ForkOptions{
		ParentSessionID: req.ParentSessionID,
		Title:           req.Title,
		TaskHint:        req.TaskHint,
		ProviderID:      req.ProviderID,
		ModelID:         req.ModelID,
		// The BranchSeamAdapter is the implicit edit-and-resend path.
		// Mark the branch row so audit consumers can distinguish it from
		// explicit "Branch from this turn" forks.
		// (branching-ux-polish-01KQ8TD7 WP01)
		CreationPath: "edit_resend",
	})
	if err != nil {
		return coreag.BranchHandle{}, fmt.Errorf("branch_seam: create branch: %w", err)
	}
	// Record the child's generation so a FUTURE Fork off this child
	// session (e.g. a spawned sub-agent dispatching another sub-agent)
	// is measured from here, not from 0.
	a.setGeneration(child.ID, childGen)
	// Seed the child session with the handoff prompt as a user message
	// so the child kernel run sees it on its first turn.
	if req.HandoffPrompt != "" {
		if _, perr := a.sessions.AppendMessage(ctx, child.ID, session.Message{
			Role:    session.RoleUser,
			Content: req.HandoffPrompt,
		}); perr != nil {
			return coreag.BranchHandle{}, fmt.Errorf("branch_seam: seed handoff: %w", perr)
		}
	}
	// Write CoW reference rows for each parent message id so the child
	// can pick them up later without copying their content.
	for _, msgID := range req.ParentMessageIDs {
		if msgID == "" {
			continue
		}
		_ = a.conversations.AppendMessageRef(ctx, br.ID, msgID)
	}

	// UNIT-6: if a run spawner is wired, ask it to start the child
	// kernel run and record the outcome for WaitForChildRun. A spawner
	// failure does NOT fail Fork itself — the branch + child session
	// already exist and are real, exactly like the pre-UNIT-6 "branch
	// already exists" reasoning for CoW ref failures above — but it IS
	// recorded, so WaitForChildRun surfaces it honestly instead of the
	// caller believing a run started when it didn't (the
	// manufactured-success class this mission exists to end).
	if spawner := a.getRunSpawner(); spawner != nil {
		run, serr := spawner(ctx, br.ID, child.ID, req)
		a.runsMu.Lock()
		if a.runs == nil {
			a.runs = make(map[string]spawnOutcome)
		}
		a.runs[br.ID] = spawnOutcome{run: run, err: serr}
		a.runsMu.Unlock()
	}

	return coreag.BranchHandle{BranchID: br.ID, ChildSessionID: child.ID}, nil
}

// PullChildTail returns the most-recent N messages on the branch's
// child session. MergeNode feeds these into compaction.
func (a *BranchSeamAdapter) PullChildTail(ctx context.Context, branchID string, n int) ([]coreag.Message, error) {
	if !a.available() {
		return nil, coreag.ErrNoBranchSeam
	}
	br, err := a.conversations.Get(ctx, branchID)
	if err != nil {
		return nil, fmt.Errorf("branch_seam: get branch %q: %w", branchID, err)
	}
	msgs, err := a.sessions.ListMessages(ctx, br.ChildSessionID)
	if err != nil {
		return nil, fmt.Errorf("branch_seam: list messages: %w", err)
	}
	if n > 0 && len(msgs) > n {
		msgs = msgs[len(msgs)-n:]
	}
	out := make([]coreag.Message, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, coreag.Message{
			Role:    string(m.Role),
			Content: m.Content,
		})
	}
	return out, nil
}

// WaitForChildRun blocks until the branch's spawned child run reaches a
// terminal state (UNIT-6), or returns immediately in every case that
// predates UNIT-6: no run spawner wired, or Fork never recorded an
// outcome for this branch (manual-merge branches created without a
// spawner-backed Fork call). This is the seam's completion signal now
// reading from a REAL run instead of always answering "done" — see the
// package doc.
func (a *BranchSeamAdapter) WaitForChildRun(ctx context.Context, branchID string) error {
	if a.getRunSpawner() == nil {
		return nil // v1 behaviour: no spawner wired, nothing to wait for.
	}
	a.runsMu.Lock()
	outcome, ok := a.runs[branchID]
	if ok {
		// Evict on read: WaitForChildRun is called at most once per
		// branch by MergeNode / the sync dispatch path, and leaving
		// entries forever would leak memory for every dispatched
		// sub-agent over a long-running process.
		delete(a.runs, branchID)
	}
	a.runsMu.Unlock()
	if !ok {
		// Fork never recorded an outcome for this branch — either it
		// predates the spawner being wired, or this branch wasn't
		// created through a spawner-backed Fork call. Fail open like
		// pre-UNIT-6 rather than block forever on a run nothing is
		// tracking.
		return nil
	}
	if outcome.err != nil {
		return fmt.Errorf("branch_seam: spawn child run: %w", outcome.err)
	}
	if outcome.run.Wait == nil {
		return nil
	}
	return outcome.run.Wait(ctx)
}

// AppendToParent appends a message to the parent session of the
// branch. role is usually "system" so the rail UI can mark the merge
// summary as a system note.
func (a *BranchSeamAdapter) AppendToParent(ctx context.Context, branchID, role, content string) (string, error) {
	if !a.available() {
		return "", coreag.ErrNoBranchSeam
	}
	br, err := a.conversations.Get(ctx, branchID)
	if err != nil {
		return "", fmt.Errorf("branch_seam: get branch %q: %w", branchID, err)
	}
	r := session.Role(role)
	if r == "" {
		r = session.RoleSystem
	}
	stored, err := a.sessions.AppendMessage(ctx, br.ParentSessionID, session.Message{
		Role:    r,
		Content: content,
	})
	if err != nil {
		return "", fmt.Errorf("branch_seam: append parent: %w", err)
	}
	return stored.ID, nil
}

// MarkMerged flips the branch row's status to merged.
func (a *BranchSeamAdapter) MarkMerged(ctx context.Context, branchID string) error {
	if !a.available() {
		return coreag.ErrNoBranchSeam
	}
	return a.conversations.MarkMerged(ctx, branchID)
}

// ActiveBranchForChildSession reports the branch whose child session is
// sessionID, when that branch is still BranchStatusActive. A session
// with no owning branch (a parent session, or any other ordinary
// session), or a branch already merging/merged/abandoned, reports ok
// false — the chat runner's merge-suggestion trigger
// (engineer-truth-pass-01PMTP01 WP08) uses this to skip every session
// that isn't a live branch child before it evaluates the heuristic.
func (a *BranchSeamAdapter) ActiveBranchForChildSession(ctx context.Context, sessionID string) (string, bool) {
	if !a.available() || sessionID == "" {
		return "", false
	}
	branches, err := a.conversations.ListByChild(ctx, sessionID)
	if err != nil {
		return "", false
	}
	for _, br := range branches {
		if br.Status == coreconv.BranchStatusActive {
			return br.ID, true
		}
	}
	return "", false
}

// Compile-time witness.
var _ coreag.BranchSeam = (*BranchSeamAdapter)(nil)
