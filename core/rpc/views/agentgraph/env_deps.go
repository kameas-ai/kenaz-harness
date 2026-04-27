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
}
