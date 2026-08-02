package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// ReasoningStyle enumerates the ways a provider exposes per-request
// reasoning effort configuration (FR-001 / FR-020 of
// provider-implementation-uniformity-01KQ8V4F).
//
// The value returned by ProviderCapabilities.Reasoning determines
// which controls the frontend renders (see WP07 ReasoningControl.vue).
type ReasoningStyle int

const (
	// ReasoningNone — the model does not expose a user-configurable
	// reasoning effort knob. The model either has no extended thinking
	// (e.g. GPT-3.5-turbo) or the reasoning is fully opaque.
	ReasoningNone ReasoningStyle = iota

	// ReasoningEffortString — the provider accepts a discrete effort
	// string: "minimal", "low", "medium", or "high". Used by OpenAI o-
	// series and o3-mini models via the `reasoning_effort` parameter.
	ReasoningEffortString

	// ReasoningTokenBudget — the provider accepts an explicit token
	// budget integer. Used by Anthropic claude-3-7-sonnet and later via
	// the `thinking.budget_tokens` parameter.
	ReasoningTokenBudget

	// ReasoningBoth — the provider accepts both an effort string and a
	// token budget (for future multi-provider normalisation). Adapters
	// that advertise Both must implement both wire shapes.
	ReasoningBoth
)

// ProviderCapabilities is the rich per-(provider, model) capability
// record introduced by FR-001 + FR-020 of
// provider-implementation-uniformity-01KQ8V4F.
//
// It extends the older CapabilityDescriptor (which is retained for
// backward compatibility) with:
//
//   - 20+ boolean flags covering every knob in the RequestKnobs surface
//   - a ReasoningStyle discriminator
//   - per-provider attachment limits (migrated from the existing
//     AttachmentDescriptor)
//
// Adapters expose ProviderCapabilities through the new async
// ProviderAdapter.Capabilities(ctx, modelID) accessor (C-004). The
// synchronous Capabilities(model string) CapabilityDescriptor shim
// remains on the interface for backward compatibility until all callers
// migrate.
type ProviderCapabilities struct {
	// Provider is the adapter kind (e.g. "openai", "anthropic").
	Provider string `json:"provider"`
	// Model is the resolved model id (e.g. "gpt-4o", "claude-sonnet-4-5").
	Model string `json:"model"`

	// ── Text generation ────────────────────────────────────────────────

	// Streaming — the adapter supports server-sent event streaming.
	// Virtually universal; false only for image-generation-only models.
	Streaming bool `json:"streaming"`
	// ToolCalling — the model supports function/tool calls.
	ToolCalling bool `json:"tool_calling"`
	// ParallelToolCalls — the model and adapter support issuing multiple
	// tool calls in a single response (OpenAI: parallel_tool_calls:true).
	ParallelToolCalls bool `json:"parallel_tool_calls"`
	// Vision — the model accepts image blocks in the message content.
	Vision bool `json:"vision"`
	// Documents — the model accepts document/PDF blocks in the message
	// content.
	Documents bool `json:"documents"`

	// ── Output format ──────────────────────────────────────────────────

	// JSONMode — the provider supports constraining the response to valid
	// JSON (response_format: json_object or equivalent).
	JSONMode bool `json:"json_mode"`
	// StructuredOutput — the provider natively supports JSON-schema-
	// constrained output (response_format: json_schema or equivalent).
	StructuredOutput bool `json:"structured_output"`
	// Grammar — the runtime supports token-level GBNF grammar constraints
	// (local runtimes only; cloud providers always return false).
	Grammar bool `json:"grammar"`
	// RegexGrammar — the runtime supports regex-shorthand grammar constraints
	// (subset of GBNF; local runtimes only).
	RegexGrammar bool `json:"regex_grammar"`
	// ResponseFormat — the adapter honours the ResponseFormat field on
	// GenerationRequest (json / json_schema / grammar). Implies at least
	// JSONMode or StructuredOutput or Grammar is true.
	ResponseFormat bool `json:"response_format"`

	// ── Sampling knobs ─────────────────────────────────────────────────

	// Seed — the provider supports the `seed` parameter for deterministic
	// outputs (OpenAI and OpenRouter; Anthropic/Bedrock: false).
	Seed bool `json:"seed"`
	// Logprobs — the provider supports log-probability output.
	Logprobs bool `json:"logprobs"`
	// TopK — the provider supports the `top_k` sampling parameter.
	TopK bool `json:"top_k"`
	// TopP — the provider supports the `top_p` sampling parameter.
	TopP bool `json:"top_p"`
	// FrequencyPenalty — the provider supports the `frequency_penalty` knob.
	FrequencyPenalty bool `json:"frequency_penalty"`
	// PresencePenalty — the provider supports the `presence_penalty` knob.
	PresencePenalty bool `json:"presence_penalty"`

	// ── Session ──────────────────────────────────────────────────────

	// Batch — the provider supports asynchronous batch inference.
	Batch bool `json:"batch"`

	// ── Extended thinking / reasoning ──────────────────────────────────

	// Reasoning — the model supports extended-thinking output (chain of
	// thought tokens). True ⟹ ReasoningStyle is not ReasoningNone.
	Reasoning bool `json:"reasoning"`
	// ReasoningStyle discriminates how the per-request reasoning effort
	// is configured (see the ReasoningStyle constants above).
	Reasoning_ ReasoningStyle `json:"reasoning_style"`

	// ── Infrastructure ─────────────────────────────────────────────────

	// Cancellation — the stream supports mid-flight cancellation within
	// the p99 ≤ 1 s NFR.
	Cancellation bool `json:"cancellation"`
	// UsageReporting — the provider returns token-usage metadata.
	UsageReporting bool `json:"usage_reporting"`
	// ImageOutput — the model supports generating images as output.
	ImageOutput bool `json:"image_output"`

	// ── Context limits ─────────────────────────────────────────────────

	// ContextWindow is the model's max context length in tokens.
	// 0 means unknown.
	ContextWindow int `json:"context_window,omitempty"`
	// MaxOutputTokens is the provider's hard cap on completion tokens
	// per turn. 0 means unknown.
	MaxOutputTokens int `json:"max_output_tokens,omitempty"`

	// Notes carries per-flag human-readable notes for debugging / docs.
	Notes map[string]string `json:"notes,omitempty"`
}

// CapabilitySchemaVersion is incremented whenever the shape of
// ProviderCapabilities changes in a way that requires cache
// invalidation (WP05 SQLite capability cache).
const CapabilitySchemaVersion = 1

// ToDescriptor converts a ProviderCapabilities record to the legacy
// CapabilityDescriptor format for backward compatibility with callers
// that haven't migrated to the richer type (C-004 deprecation shim).
func (c ProviderCapabilities) ToDescriptor() CapabilityDescriptor {
	sup := map[Capability]bool{
		CapStreaming:        c.Streaming,
		CapToolCalling:      c.ToolCalling,
		CapVision:           c.Vision,
		CapDocuments:        c.Documents,
		CapJSONMode:         c.JSONMode,
		CapReasoning:        c.Reasoning,
		CapCancellation:     c.Cancellation,
		CapUsageReporting:   c.UsageReporting,
		CapStructuredOutput: c.StructuredOutput,
		CapGrammar:          c.Grammar,
		CapRegexGrammar:     c.RegexGrammar,
		CapImageOutput:      c.ImageOutput,
	}
	return CapabilityDescriptor{
		Provider:  c.Provider,
		Model:     c.Model,
		Supported: sup,
		Notes:     c.legacyNotes(),
	}
}

func (c ProviderCapabilities) legacyNotes() map[Capability]string {
	if len(c.Notes) == 0 {
		return nil
	}
	out := make(map[Capability]string, len(c.Notes))
	for k, v := range c.Notes {
		out[Capability(k)] = v
	}
	return out
}

// ─── ReasoningConfig ─────────────────────────────────────────────────────────

// ReasoningConfig is a tagged union representing the per-request
// reasoning effort configuration (FR-017). Exactly one field must be
// set; Validate() enforces this.
//
// The concrete field to populate depends on the model's ReasoningStyle:
//
//   - ReasoningEffortString → set OpenAIEffort
//   - ReasoningTokenBudget  → set AnthropicThinkingBudget
//   - ReasoningBoth         → set either or both
type ReasoningConfig struct {
	// OpenAIEffort is the discrete reasoning effort string sent to
	// OpenAI o-series models via reasoning_effort. Valid values:
	// "minimal", "low", "medium", "high".
	OpenAIEffort string `json:"openai_effort,omitempty"`
	// AnthropicThinkingBudget is the extended thinking token budget sent
	// to Anthropic models that support thinking blocks.
	AnthropicThinkingBudget int `json:"anthropic_thinking_budget,omitempty"`
}

// Validate returns an error when no field is set or when the values
// are out of range.
func (r ReasoningConfig) Validate() error {
	hasEffort := r.OpenAIEffort != ""
	hasBudget := r.AnthropicThinkingBudget > 0
	if !hasEffort && !hasBudget {
		return errors.New("llm: ReasoningConfig: at least one of OpenAIEffort or AnthropicThinkingBudget must be set")
	}
	if hasEffort {
		switch r.OpenAIEffort {
		case "minimal", "low", "medium", "high":
		default:
			return fmt.Errorf("llm: ReasoningConfig: invalid OpenAIEffort %q (must be minimal|low|medium|high)", r.OpenAIEffort)
		}
	}
	if hasBudget && r.AnthropicThinkingBudget < 1 {
		return fmt.Errorf("llm: ReasoningConfig: AnthropicThinkingBudget must be > 0, got %d", r.AnthropicThinkingBudget)
	}
	return nil
}

// ─── RequestKnobs ─────────────────────────────────────────────────────────────

// RequestKnobs is the typed configuration surface for per-request
// fine-tuning knobs (FR-017 of provider-implementation-uniformity-
// 01KQ8V4F).
//
// Knobs that are not supported by the target (provider, model) are
// handled by the per-knob fallback policy (WP08 KnobPolicy): either
// dropped silently with an audit event, mapped to the closest
// equivalent, or refused with ErrUnsupportedFeature before the wire
// call.
//
// VendorExtensions carries arbitrary provider-specific overrides
// forwarded verbatim to the adapter's wire body. Keys must be
// namespaced (e.g. "anthropic.top_k") to avoid collisions.
type RequestKnobs struct {
	// Reasoning configures per-request extended-thinking effort.
	// Nil means "use the model default / no explicit configuration."
	Reasoning *ReasoningConfig `json:"reasoning,omitempty"`

	// Seed requests a deterministic output for providers that support it
	// (OpenAI, OpenRouter). Nil means "let the provider choose."
	Seed *int `json:"seed,omitempty"`

	// ResponseFormatMode overrides the response format for this request.
	// Values: "json" | "json_schema" | "grammar" | "" (no override).
	ResponseFormatMode string `json:"response_format_mode,omitempty"`

	// JSONMode overrides JSON-mode for this request. Nil means inherit
	// from session / global settings.
	JSONMode *bool `json:"json_mode,omitempty"`

	// Temperature overrides the sampling temperature (0.0–2.0).
	// Nil means inherit.
	Temperature *float64 `json:"temperature,omitempty"`

	// TopP overrides the nucleus sampling parameter (0.0–1.0).
	// Nil means inherit.
	TopP *float64 `json:"top_p,omitempty"`

	// TopK overrides the top-k sampling parameter.
	// Nil means inherit.
	TopK *int `json:"top_k,omitempty"`

	// MaxTokens overrides the maximum completion tokens for this request.
	// 0 means inherit.
	MaxTokens int `json:"max_tokens,omitempty"`

	// FrequencyPenalty overrides the frequency penalty (-2.0–2.0).
	// Nil means inherit.
	FrequencyPenalty *float64 `json:"frequency_penalty,omitempty"`

	// PresencePenalty overrides the presence penalty (-2.0–2.0).
	// Nil means inherit.
	PresencePenalty *float64 `json:"presence_penalty,omitempty"`

	// ParallelToolCalls, when set, overrides the provider's default
	// parallel tool-call behaviour. Nil means "use provider default."
	ParallelToolCalls *bool `json:"parallel_tool_calls,omitempty"`

	// StopSequences overrides the sequences that stop generation when
	// produced. Nil/empty means "no custom stop sequences; use provider
	// default."
	StopSequences []string `json:"stop_sequences,omitempty"`

	// VendorExtensions carries arbitrary provider-specific wire-level
	// overrides forwarded verbatim to the adapter. Keys should be
	// namespaced (e.g. "anthropic.top_k"). The adapter is responsible
	// for merging these into the request body; unknown keys are ignored
	// by providers that don't recognise them.
	VendorExtensions map[string]json.RawMessage `json:"vendor_extensions,omitempty"`
}

// ─── ProviderAdapter extension ────────────────────────────────────────────────

// CapabilitiesProvider is the optional interface adapters implement to
// expose the richer ProviderCapabilities (FR-001 + FR-020). Adapters
// that implement this interface are preferred over the legacy
// CapabilityDescriptor path when the registry dispatches requests.
//
// The async signature allows adapters to perform a lazy /models
// endpoint call without blocking the synchronous Capabilities shim.
// The caller must supply a reasonable deadline via ctx.
type CapabilitiesProvider interface {
	// ProviderCapabilities returns the rich capability record for the
	// given model. When the model is unknown the adapter returns a
	// conservative curated default and emits an audit event.
	ProviderCapabilities(ctx context.Context, modelID string) (ProviderCapabilities, error)
}
