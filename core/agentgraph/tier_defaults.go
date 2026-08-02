package agentgraph

// tier_defaults.go — versioned-model-profile-01PMDL04 WP05.
//
// The compute-primitive schema has long supported per-node Verbosity
// (PlannerAttrs) and per-node MaxIterations (ReviewAttrs, ReflectAttrs),
// but every shipped graph either hardcodes them or the executor falls
// back to one fixed default regardless of which model is actually
// executing the node. This file gives the compute executors a single,
// data-driven place to resolve those defaults from the active model's
// size tier when a node's author leaves the attr unset (zero value).
// An explicit author value is read before any of this runs and always
// wins — see plannerExecutor / reviewExecutor / reflectExecutor in
// exec_compute.go.
//
// Consistent with the frozen-core discipline WP04 established: this
// file asks a data question through the TierSource seam (backed by
// core/llm/capabilities' Catalog via rpc wiring) instead of string-
// matching model family names itself.

// ActiveModelSource is an optional interface an Env.LLM provider may
// implement to expose the concrete (provider kind, model id) it will
// dispatch a request to when a node's own Provider/Model override is
// empty (or the "default" sentinel shipped graphs use to mean "no
// override"). resolveNodeTier type-asserts env.LLM against this
// interface; providers that don't implement it (test fakes, scripted
// activities, LLMProviderFunc closures) simply contribute no opinion
// and the caller's ModelTierMedium fallback applies — so adding this
// interface can only ever add tier information, never take any away.
type ActiveModelSource interface {
	// ProviderKind returns the active provider's kind (e.g. "anthropic",
	// "openai", "bedrock"), or "" when unknown.
	ProviderKind() string
	// ActiveModelID returns the effective model id the next unqualified
	// request will dispatch to, or "" when unknown.
	ActiveModelID() string
}

// resolveNodeTier resolves the size tier for a node's active (provider,
// model) pair. provider/model are the node's own explicit attrs (may be
// empty); when model is empty or the "default" sentinel, it falls back
// to env.LLM's ActiveModelSource (when the provider implements it) to
// find the run's actual default model. Returns ModelTierMedium whenever
// no tier data is available anywhere — nil env, nil env.TierSource, no
// ActiveModelSource, or a TierSource with no opinion on the resolved
// model — which is exactly today's hardcoded behaviour, so callers with
// no tier data see no change.
func resolveNodeTier(env *Env, provider, model string) ModelTier {
	if env == nil {
		return ModelTierMedium
	}
	if model == "" || model == "default" {
		if src, ok := env.LLM.(ActiveModelSource); ok {
			if provider == "" {
				provider = src.ProviderKind()
			}
			if m := src.ActiveModelID(); m != "" {
				model = m
			}
		}
	}
	if model == "" || model == "default" || env.TierSource == nil {
		return ModelTierMedium
	}
	if t, ok := env.TierSource.Tier(provider, model); ok {
		return t
	}
	return ModelTierMedium
}

// verbosityForTier maps a resolved model tier to the PlannerAttrs
// Verbosity default applied when a node leaves `verbosity` unset. A
// smaller/cheaper model gets a terser plan (less budget to sustain a
// long multi-step decomposition); a larger model gets a verbose plan
// (it can make use of the extra reasoning surface). Medium — and any
// tier this package doesn't know about — keeps the "standard" value the
// executor hardcoded before WP05, so the common case (no tier data
// available) is unchanged.
func verbosityForTier(t ModelTier) string {
	switch t {
	case ModelTierSmall:
		return "terse"
	case ModelTierLarge:
		return "verbose"
	default:
		return "standard"
	}
}

// maxIterationsForTier maps a resolved model tier to the ReviewAttrs /
// ReflectAttrs MaxIterations default applied when a node leaves
// `max_iterations` unset (<= 0). A smaller/less reliable model is given
// more passes to converge on a passing verdict; a larger model is
// expected to need fewer. Medium keeps review.yaml's pre-existing
// explicit default (3) so a node with no tier data behaves the same as
// today's shipped activity.
func maxIterationsForTier(t ModelTier) int {
	switch t {
	case ModelTierSmall:
		return 5
	case ModelTierLarge:
		return 2
	default:
		return 3
	}
}

// ContextWindowSource resolves the maximum context length, in tokens,
// for a (providerKind, modelID) pair. It mirrors TierSource: the frozen
// core asks a data question through a seam rather than knowing anything
// about specific models. Backed in production by core/llm/capabilities'
// curated catalog; nil in tests and scripted runs, where a zero window
// makes the automatic compaction sites skip rather than guess.
type ContextWindowSource interface {
	// ContextWindow returns the window in tokens, or 0 when unknown.
	ContextWindow(providerKind, modelID string) int
}

// resolveContextWindow returns the active model's context window, or 0
// when it cannot be determined. Mirrors resolveNodeTier's fallbacks: an
// explicit node model wins, otherwise the run's ActiveModelSource is
// consulted, and any missing piece yields 0 ("unknown") rather than a
// guessed default — a wrong window would drive compaction to the wrong
// target, which is worse than not compacting.
func resolveContextWindow(env *Env, provider, model string) int {
	if env == nil || env.ContextWindows == nil {
		return 0
	}
	if model == "" || model == "default" {
		if src, ok := env.LLM.(ActiveModelSource); ok {
			if provider == "" {
				provider = src.ProviderKind()
			}
			if m := src.ActiveModelID(); m != "" {
				model = m
			}
		}
	}
	if model == "" || model == "default" {
		return 0
	}
	return env.ContextWindows.ContextWindow(provider, model)
}
