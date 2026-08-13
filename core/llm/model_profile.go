package llm

import (
	"reflect"

	"github.com/kameas-ai/kenaz-harness/core/compactionpolicy"
)

// ModelProfile is the versioned, hot-swappable *behavioral* artifact for a
// model or model family — the harness-side mirror of a LoRA
// (versioned-model-profile-01PMDL04 spec §4). It is deliberately split from
// ProviderProfile (llm.go:113), which stays pure connection/credential
// config (endpoint, auth, region, kind). ModelProfile carries the
// behavioral knobs a model needs re-tuned for as new revisions ship:
// prompt-template selection, tool-calling dialect, context/compaction
// policy, the retry/recovery ladder, and an eval-fit manifest reference.
//
// Connection and behavior resolve independently by design (spec §5 /
// plan "conflict zones"): rotating a credential must never force a
// behavioral re-promotion, and activating a new ModelProfile version must
// never touch credential state. Nothing in core/llm wires this type into
// a runtime path yet — resolution is WP02 ((model_id, version) lookup with
// family-glob inheritance, mirroring capabilities/loader.go's Match
// pattern), eval-fit gating is WP03. The layering validator that rejects
// Cedar/budget-policy fields smuggled into a profile bundle is
// ValidateModelProfileBundle (model_profile_schema.go, WP06) — it must be
// the entry point any future bundle-parsing seam (WP02) uses for
// ModelProfile bundles, since it operates on raw bytes before unknown
// keys are silently dropped by unmarshal.
//
// Zero value is valid and inert: an absent ModelProfile must mean "today's
// behavior", never a forced default. Every field is a pointer or omittable
// so a caller that never populates one gets literally no behavior change.
// IsZero/ValidateModelProfile both special-case the zero value.
type ModelProfile struct {
	// ID identifies the profile family this artifact applies to. WP02's
	// resolver keys off (ID, Version); ID may itself be a family-glob
	// (e.g. "claude-sonnet-*") mirroring capabilities/loader.go's Match
	// field so inheritance is mechanically possible without a new pattern
	// language.
	ID string `json:"id" yaml:"id"`

	// Version is the explicit version of this behavioral artifact. This is
	// the field ProviderProfile has never had (spec problem statement) —
	// it is what makes the profile a promotable, rollback-able revision
	// rather than a flat last-write-wins config blob.
	Version string `json:"version" yaml:"version"`

	// PromptTemplate references the versioned prompt-template/shaping
	// config this profile renders system prompts with. The rendering
	// *mechanism* (per-family XML vs Markdown selection, attention
	// placement, few-shot priming) is per-family-message-shaping-01PMDL06;
	// this field only gives that mechanism a versioned home to read from.
	PromptTemplate *PromptTemplateRef `json:"prompt_template,omitempty" yaml:"prompt_template,omitempty"`

	// ToolDialect configures how tool calls are offered to / parsed from
	// this model family (native function-calling vs a synthetic
	// XML/JSON convention for models without native tool support, tool
	// description verbosity, parallel-call support).
	ToolDialect *ToolDialectConfig `json:"tool_dialect,omitempty" yaml:"tool_dialect,omitempty"`

	// Context carries the context-window / compaction policy this model
	// should run under. Aggressiveness reuses
	// core/compactionpolicy.CompactionAggressiveness directly (the tier table's
	// single source of truth) rather than a parallel enum.
	Context *ContextPolicy `json:"context,omitempty" yaml:"context,omitempty"`

	// Retry is the request-retry rung of the ladder. It deliberately
	// reuses llm.RetryPolicy (llm.go:101) — the same struct
	// ProviderProfile.Retry already uses — rather than a parallel type,
	// per the mission's "reuse, don't reinvent" directive. A profile's
	// Retry is a behavioral default; ProviderProfile.Retry / a request's
	// RetryOverride still win when set (resolution precedence is WP02+).
	Retry *RetryPolicy `json:"retry,omitempty" yaml:"retry,omitempty"`

	// FallbackChainId is the recovery rung of the ladder: a reference to a
	// core/llm/fallback.Chain.ID. It is a string reference rather than an
	// embedded fallback.Chain because core/llm/fallback already imports
	// core/llm (evaluator.go, runner.go) — embedding the struct here would
	// create an import cycle. This mirrors the existing
	// agentgraph node-attr convention (FallbackChainId string,
	// model-fallback-routing-01NDFSEX04) of referencing chains by id.
	FallbackChainId string `json:"fallback_chain_id,omitempty" yaml:"fallback_chain_id,omitempty"`

	// EvalManifest references the per-workflow eval-fit suite (core/eval)
	// a candidate version of this profile must pass before promotion.
	// WP03 wires the actual gate against core/eval's MatrixCase/RunMatrix;
	// this field only names the manifest.
	EvalManifest *EvalManifestRef `json:"eval_manifest,omitempty" yaml:"eval_manifest,omitempty"`
}

// IsZero reports whether p is the zero value — i.e. "no profile
// configured; behave exactly as today." Callers resolving a profile
// (WP02+) must treat IsZero()==true as "fall through to
// ProviderProfile-only behavior", never as a validation failure.
func (p ModelProfile) IsZero() bool {
	return reflect.DeepEqual(p, ModelProfile{})
}

// PromptTemplateRef points at a versioned prompt-template/shaping-config
// artifact. See per-family-message-shaping-01PMDL06 for the consuming
// mechanism.
type PromptTemplateRef struct {
	// ID identifies the template (or template family) this profile uses.
	ID string `json:"id" yaml:"id"`
	// Version is the template's own version, independent of the owning
	// ModelProfile.Version (templates and profiles may revise on
	// different cadences). Empty means "latest".
	Version string `json:"version,omitempty" yaml:"version,omitempty"`
	// Format is an optional rendering-variant hint (e.g. "xml",
	// "markdown"). Empty means "use the default renderer" — today's
	// plain-join behavior, unchanged (01PMDL06 spec §5 byte-identity
	// requirement for un-profiled models).
	Format string `json:"format,omitempty" yaml:"format,omitempty"`
	// AttentionPlacement opts into primacy/recency duplication of
	// critical instructions. Off by default (01PMDL06 spec §4: "off by
	// default, profile-driven").
	AttentionPlacement bool `json:"attention_placement,omitempty" yaml:"attention_placement,omitempty"`
}

// ToolDialectConfig configures how tool calls are rendered to / parsed
// from a model family.
type ToolDialectConfig struct {
	// Dialect selects the tool-calling convention (e.g. "native",
	// "xml", "json_prompt"). Empty means "adapter default" — today's
	// behavior of using native tool_calling when the capability
	// descriptor supports it.
	Dialect string `json:"dialect,omitempty" yaml:"dialect,omitempty"`
	// ParallelToolCalls indicates whether this model family supports /
	// should be offered parallel (multi-call) tool invocation in one
	// turn. Nil means "adapter default".
	ParallelToolCalls *bool `json:"parallel_tool_calls,omitempty" yaml:"parallel_tool_calls,omitempty"`
	// MaxToolDescriptionBytes truncates tool Description text for
	// context-constrained models. Zero means "no truncation" (today's
	// behavior — descriptions forwarded byte-for-byte).
	MaxToolDescriptionBytes int `json:"max_tool_description_bytes,omitempty" yaml:"max_tool_description_bytes,omitempty"`
}

// ContextPolicy carries the context-window / compaction behavior a model
// profile prescribes.
type ContextPolicy struct {
	// Aggressiveness selects the compaction tier, reusing
	// core/compactionpolicy.CompactionAggressiveness directly rather than a
	// parallel enum. Empty means "session/app default" (today's
	// behavior — compactionpolicy.Tier's own fallback to "balanced" applies
	// unchanged when a caller reads this field as unset).
	Aggressiveness compactionpolicy.CompactionAggressiveness `json:"aggressiveness,omitempty" yaml:"aggressiveness,omitempty"`
	// ContextWindowOverride optionally overrides the model's advertised
	// context window (tokens) for compaction trigger math — e.g. to
	// reserve headroom below the capability catalog's window. Zero
	// means "use the capability descriptor's window" (today's
	// behavior).
	ContextWindowOverride int `json:"context_window_override,omitempty" yaml:"context_window_override,omitempty"`
}

// EvalManifestRef points at a core/eval eval-fit manifest — the
// per-workflow regression suite a candidate profile version must pass
// before a bundle-activation promotes it (WP03).
type EvalManifestRef struct {
	// ID identifies the manifest (e.g. a workflow/suite name).
	ID string `json:"id" yaml:"id"`
	// Version is the manifest's own version, independent of the owning
	// ModelProfile.Version. Empty means "latest".
	Version string `json:"version,omitempty" yaml:"version,omitempty"`
}
