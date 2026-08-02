package agentgraph

// prompt_render.go — per-family-message-shaping-01PMDL06 WP01.
//
// prompts.Compose (core/agentgraph/prompts) now accepts an optional
// *corellm.PromptTemplateRef to select a per-family renderer. This file
// gives the compute executors a seam to resolve that descriptor for a
// node's active (provider, model) pair, mirroring resolveNodeTier /
// resolveContextWindow (tier_defaults.go) exactly: the frozen core asks
// a data question through an interface rather than string-matching
// model-family names itself (scripts/ci/check-no-model-family-
// literals.sh forbids that repo-wide outside core/llm/**).
//
// Nothing implements PromptTemplateSource in production yet — resolving
// a live ModelProfile is later work (versioned-model-profile-01PMDL04
// WP02+; the spec for this mission explicitly allows deferring that
// wiring rather than half-doing it). A nil env.PromptTemplates — true
// for every run today — makes resolvePromptTemplate always return nil,
// which is prompts.Compose's byte-identical default. Once a real
// (providerKind, modelID) -> ModelProfile lookup exists, an adapter
// satisfying this interface plugs in at the rpc wiring layer
// (core/rpc/views/agentgraph/env_deps.go), the same way
// tierSourceAdapter/ContextWindows adapters do today.

import (
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// PromptTemplateSource resolves the prompt-rendering template a
// (providerKind, modelID) pair's ModelProfile prescribes.
type PromptTemplateSource interface {
	// PromptTemplate returns the template/variant reference for the
	// pair, or nil when no profile applies (selects the default
	// renderer).
	PromptTemplate(providerKind, modelID string) *corellm.PromptTemplateRef
}

// resolvePromptTemplate resolves the prompt-template descriptor for a
// node's active (provider, model) pair. provider/model are the node's
// own explicit attrs (may be empty); when model is empty or the
// "default" sentinel, it falls back to env.LLM's ActiveModelSource
// (when implemented) to find the run's actual default model — the same
// fallback shape as resolveNodeTier. Returns nil whenever no source is
// configured or has no opinion, which selects prompts.Compose's default
// Markdown-join renderer — exactly today's behaviour.
func resolvePromptTemplate(env *Env, provider, model string) *corellm.PromptTemplateRef {
	if env == nil || env.PromptTemplates == nil {
		return nil
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
		return nil
	}
	return env.PromptTemplates.PromptTemplate(provider, model)
}
