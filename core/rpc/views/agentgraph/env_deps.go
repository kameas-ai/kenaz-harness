package agentgraph

import (
	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
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

	// MergeSuggester evaluates the "does this branch look ready to
	// merge?" heuristic (engineer-truth-pass-01PMTP01 WP08, finding
	// B16). nil disables the branches:merge-suggested broker topic
	// entirely — the chat runner's post-run trigger no-ops without it,
	// same as every other unset EnvDeps seam.
	MergeSuggester *coreag.MergeSuggester

	// LifecycleHooks fires the v2 pre_tool_use / post_tool_use /
	// post_tool_use_failure hooks at the tool-dispatch boundary
	// (trust-surfaces-that-fire-01PMZ202 WP09 / UNIT-8). nil leaves
	// env.LifecycleHooks unset, so tool_invocation.go's nil-guards
	// short-circuit and no v2 lifecycle hook ever fires — this was the
	// production default before WP09: the seam existed and read from
	// three call sites, but core/hooks.LifecycleRunnerAdapter, the only
	// implementation, was never constructed.
	LifecycleHooks coreag.LifecycleHookRunner
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

// WithGraphCedarGate supplies the graph.author / graph.run policy gate
// (model-authored-graphs-01PMGA01 UNIT-4). Not calling this option
// leaves cedarGate nil, which GateGraphAuthor/GateGraphRun's own
// nil-gate contract maps to default-allow — the correct behaviour for
// a library caller (tests), the wrong one for production. Production
// wiring (core/rpc/api.go's newGraphManagerWithDeps) must pass the same
// process-shared engine deps.Policy already uses; I13 clause 4
// (check-cedar-gate-arguments.sh, UNIT-8(b)) is the CI gate that keeps
// that argument from silently going missing.
// It also supplies the gate for graph.run and for approval.resolve
// (approval-node-01PMZC12 UNIT-3). ZC12 originally shipped a SECOND
// option, WithCedarGate, writing the SAME m.cedarGate field; production
// passed both, with the same value, in one mgrOpts slice. Harmless until
// the two ever diverged, at which point it would have been silent
// last-write-wins.
//
// That duplication also cost a real test: a deny-everything fake set
// through one option denied graph.run as well, so ZC12's
// approval-deny test failed before the approval node ever fired, for a
// reason unrelated to approvals. Collapsed to one option after the
// independent review of PR #306 (finding N3).
func WithGraphCedarGate(g cedar.Gate) ManagerOption {
	return func(m *Manager) { m.cedarGate = g }
}

// WithAuthoringEnabled supplies the FR-006 consent-dial resolver read
// live on every graph.author evaluation. Same read-at-consumption shape
// as WithAgenticTurnRouting above, for the same reason: a toggle in
// Settings must take effect on the next draft attempt without an app
// restart. Not calling this option leaves authoringEnabled nil, which
// saveGraph treats as OFF — the shipped default and the fail-closed
// reading for a Manager with no settings store behind it.
func WithAuthoringEnabled(enabled func() bool) ManagerOption {
	return func(m *Manager) { m.authoringEnabled = enabled }
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
	if d.MergeSuggester != nil {
		env.MergeSuggester = d.MergeSuggester
	}
	if d.LifecycleHooks != nil {
		env.LifecycleHooks = d.LifecycleHooks
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
