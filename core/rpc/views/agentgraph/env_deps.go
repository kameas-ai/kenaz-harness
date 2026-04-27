package agentgraph

import (
	coreag "github.com/sigil-tech/kaneaz-harness/core/agentgraph"
	corebash "github.com/sigil-tech/kaneaz-harness/core/tools/bash"
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
}
