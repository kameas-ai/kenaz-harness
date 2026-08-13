package agentgraph

import (
	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	corebash "github.com/kameas-ai/kenaz-harness/core/tools/bash"
)

// EnvDeps bundles the production seams the graph kernel's Env needs.
// The view's Manager.startRun copies these into the per-run Env so the
// kernel's executors no longer fall through to the agentgraph nil-stubs.
//
// Every field is optional. Unset fields fall back to the agentgraph
// nil-stub (errors with ErrNoCorpus / ErrNoBranchSeam / etc.) so test
// harness callers that don't wire a real subsystem keep working — the
// kernel's applyEnvDefaults stays the SAFE FALLBACK path, no longer
// the production path.
//
// IMPORTANT (DIRECTIVE_001): the manager packages (core/conversation,
// core/corpus, core/memory, core/policy/cedar, core/tools/bash) do NOT
// import core/rpc/views/agentgraph. The wiring direction is one-way:
// the chassis imports the manager packages and constructs adapters
// (env_deps_*.go) that satisfy the agentgraph seams.
type EnvDeps struct {
	Branch     coreag.BranchSeam
	Corpus     coreag.CorpusBackend
	Memory     coreag.MemoryStore
	BashOutput coreag.BashOutputStore
	Policy     coreag.PolicyGate
	// BashStore is the per-process cache the bash tool writes into.
	// Held here so the Manager can surface it to a caller that wants
	// to inspect run-ids in tests; not consumed by the kernel directly.
	BashStore *corebash.Store
	// JournalWriter persists the memory hook journal to migration
	// 0308's memory_hook_journal table. Threaded onto the kernel's
	// HookManager via SetJournalWriter after applyEnvDefaults
	// constructs it. nil disables persistence (the in-memory ring
	// buffer continues to work).
	JournalWriter coreag.JournalWriter
	// History is the read-side session-history seam consumed by the
	// HistoryReadNode kind. nil installs the agentgraph nil-stub
	// (returns an empty history).
	History coreag.HistoryReader
	// HistoryWriter is the write-side seam consumed by the
	// SessionWriteNode kind (chat-migration WP02). nil installs the
	// nilHistoryWriter stub which errors on every call.
	HistoryWriter coreag.HistoryWriter
	// TierSource resolves a model tier for (providerKind, modelID) pairs
	// so the Planner/Review/Reflect executors can derive a Verbosity /
	// MaxIterations default when a node leaves the attr unset
	// (versioned-model-profile-01PMDL04 WP05). nil disables tier-derived
	// defaulting — the executors fall back to ModelTierMedium, matching
	// their pre-WP05 hardcoded defaults.
	TierSource coreag.TierSource

	// ContextWindows resolves the active model's context window so the
	// compaction pipeline can fire as the conversation approaches the
	// limit. nil ⇒ unknown ⇒ automatic compaction skips.
	ContextWindows coreag.ContextWindowSource

	// ToolOutputArchive persists the full payload of a tool result the
	// kernel truncated at dispatch (turn-context-runway-01PMAG03 WP01)
	// so the elision marker can name a resolvable handle. nil keeps the
	// truncation and drops the full payload.
	ToolOutputArchive coreag.ToolOutputArchive
}

// WithEnvDeps installs production seams onto the Manager. The seams
// are copied into every Env constructed by startRun. Use it from the
// chassis (core/rpc/api.go) to bind real managers (conversation,
// corpus, memory, cedar) into the kernel's runtime path.
//
// Calling WithEnvDeps does not retroactively affect already-running
// runs; the seams take effect on the next StartRun.
func WithEnvDeps(deps EnvDeps) ManagerOption {
	return func(m *Manager) {
		m.envDeps = deps
	}
}

// WithAgenticTurnRouting supplies the launch-flag resolver the Graphs
// view's Run button consults before executing chat_default
// (agentgraph-total-convergence-01PMGX01 WP11b, review finding N5).
//
// It is a FUNC, not a bool, for the same reason the chat side reads the
// dial per StartStream: read-at-consumption means flipping the lever
// takes effect on the next run rather than the next launch. Not calling
// this option leaves the flag off, which is the shipped default and the
// safe reading for a Manager with no settings store behind it.
func WithAgenticTurnRouting(enabled func() bool) ManagerOption {
	return func(m *Manager) {
		m.routingEnabled = enabled
	}
}

// applyEnvDeps mutates env in-place, threading every non-nil seam from
// EnvDeps into the kernel's Env. Empty deps = noop; the kernel falls
// through to applyEnvDefaults which installs nil-stubs.
func (d EnvDeps) applyTo(env *coreag.Env) {
	if env == nil {
		return
	}
	if d.Branch != nil {
		env.Branch = d.Branch
	}
	if d.Corpus != nil {
		env.Corpus = d.Corpus
	}
	if d.Memory != nil {
		env.Memory = d.Memory
	}
	if d.BashOutput != nil {
		env.BashOutput = d.BashOutput
	}
	if d.Policy != nil {
		env.Policy = d.Policy
	}
	// JournalWriter is applied AFTER the kernel constructs its
	// HookManager (kernel.Run does that lazily on the first fire).
	// We can't reach the HookManager here because env.Hooks is still
	// nil at this point. The kernel's applyEnvDefaults reads back the
	// EnvDeps via the env.Hooks creation path; alternatively we wire
	// it through env.JournalWriter which the kernel's HookManager
	// init pulls in. The agentgraph.Env carries the seam directly.
	if d.JournalWriter != nil {
		env.JournalWriter = d.JournalWriter
	}
	if d.History != nil {
		env.History = d.History
	}
	if d.HistoryWriter != nil {
		env.HistoryWriter = d.HistoryWriter
	}
	if d.ContextWindows != nil {
		env.ContextWindows = d.ContextWindows
	}
	if d.TierSource != nil {
		env.TierSource = d.TierSource
	}
	if d.ToolOutputArchive != nil {
		env.ToolOutputArchive = d.ToolOutputArchive
	}

	// Arm the growth watermark for every graph-authored run that did not
	// bring its own (agentgraph-total-convergence-01PMGX01 WP08 review,
	// finding F2).
	//
	// A nil Env.AutoCompaction means "no watermark, no gate" — every
	// automatic compaction site fires on its first visit. That was the
	// correct reading while the FR-041 presets disabled SitePreCall at
	// every dial tier: an ungated site that is also switched off cannot
	// fire. WP08 enabled the site at every tier but "off", which turned
	// the nil default into real behaviour: a workflow, a delegate run or
	// a user-authored graph would compact at its very first model call,
	// before the run had accumulated any context to shed, spending a
	// compaction pass to shrink a transcript that had not grown.
	//
	// The chat surface has always armed one (chat_runner.go sets it on
	// the Env it builds, before this callback runs — hence the nil check
	// rather than an unconditional assignment). This gives every other
	// run the same contract: the first observation latches the run's
	// baseline and is refused by construction, and a later site fires
	// only once the live context has grown past that baseline by the
	// policy margin.
	//
	// The zero policy selects the package defaults, which is the same
	// policy chat gets when its own dial leaves the knobs unset.
	if env.AutoCompaction == nil {
		env.AutoCompaction = coreag.NewCompactionWatermark(coreag.CompactionWatermarkPolicy{})
	}
}
