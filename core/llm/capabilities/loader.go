// Package capabilities loads per-provider capability descriptors from
// YAML data files (plan §R4: descriptors live as data, not code) and
// gates outgoing requests against them (FR-013).
package capabilities

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

//go:embed data/*.yaml
var dataFS embed.FS

// providerSpec is the on-disk schema (capabilities/data/<provider>.yaml).
type providerSpec struct {
	Provider string         `yaml:"provider"`
	Defaults map[string]any `yaml:"defaults"`
	Models   []modelEntry   `yaml:"models"`
	// Tiers is the provider-owned size-tier table (versioned-model-profile-
	// 01PMDL04 WP04): it replaces the frozen-core `tierFromModelID` name
	// matching that used to live in core/agentgraph. Matched independently
	// of Models (own list, own match loop) so tier assignment never
	// perturbs the existing capability glob rows or their match order.
	Tiers []tierEntry `yaml:"tiers"`
}

// tierEntry is one row in a provider's tiers: table.
//
// Match is either a concrete model ID (no "*") — a real, enumerable
// candidate model surfaced via Catalog.KnownModels — or a prefix/suffix
// glob ("claude-haiku-*") used only to classify a model the caller
// already knows the ID of (Catalog.Tier / Catalog.TierAny).
type tierEntry struct {
	Match string `yaml:"match"`
	Tier  string `yaml:"tier"`
}

// modelEntry is one per-model override row in a provider YAML.
//
// The capability/knob flags are `*bool` (rather than `bool`) so the loader
// can distinguish "this row explicitly sets the field" (non-nil) from
// "this row is silent on the field" (nil) — the latter must inherit the
// provider-level `defaults:` value instead of zeroing it out. See
// applyRichEntry / Describe, which merge on that distinction. Integer/slice
// fields carry attachment limits (multimodal-io-01KQ8TDF FR-005) and are
// unaffected (they already merge via a nonzero/non-empty check).
type modelEntry struct {
	Match          string `yaml:"match"`
	Streaming      *bool  `yaml:"streaming"`
	ToolCalling    *bool  `yaml:"tool_calling"`
	Vision         *bool  `yaml:"vision"`
	Documents      *bool  `yaml:"documents"`
	JSONMode       *bool  `yaml:"json_mode"`
	Reasoning      *bool  `yaml:"reasoning"`
	Cancellation   *bool  `yaml:"cancellation"`
	UsageReporting *bool  `yaml:"usage_reporting"`
	// StructuredOutput is true when the model/provider supports native
	// JSON-schema-constrained output (response_format or tool-call workaround
	// with validated extraction). (structured-output-and-grammar-01KX5R8A FR-002)
	StructuredOutput *bool `yaml:"structured_output"`
	// Grammar is true when the model/runtime supports token-level GBNF
	// grammar constraints. True only for local runtimes (llama.cpp / Ollama).
	// (structured-output-and-grammar-01KX5R8A FR-002)
	Grammar *bool `yaml:"grammar"`
	// RegexGrammar is true when the model/runtime supports regex-shorthand
	// grammar constraints. (structured-output-and-grammar-01KX5R8A FR-002)
	RegexGrammar *bool `yaml:"regex_grammar"`
	// ImageOutput is true when the model/provider supports generating images
	// as output (e.g. DALL-E 3, gpt-image-1, Amazon Titan Image).
	// (multimodal-io-extended-01KQ8TD2 WP05)
	ImageOutput *bool `yaml:"image_output"`
	// ContextWindow is the model's max context length in tokens (0 = unknown).
	ContextWindow int `yaml:"context_window"`
	// MaxOutputTokens is the provider's hard cap on completion tokens per
	// turn (0 = unknown). Sourced from provider documentation; surfaced in
	// ModelInfo for the frontend context-window indicator
	// (backend-context-window-length-01KQ8TD3 WP01).
	MaxOutputTokens int `yaml:"max_output_tokens"`

	// ── Extended knob capabilities (provider-implementation-uniformity-01KQ8V4F WP06) ──

	// ParallelToolCalls is true when the model/provider supports issuing
	// multiple tool calls in a single response (OpenAI parallel_tool_calls).
	ParallelToolCalls *bool `yaml:"parallel_tool_calls"`
	// Seed is true when the provider accepts the seed parameter for
	// deterministic outputs (OpenAI, OpenRouter; not Anthropic/Bedrock).
	Seed *bool `yaml:"seed"`
	// Logprobs is true when the provider supports log-probability output.
	Logprobs *bool `yaml:"logprobs"`
	// TopK is true when the provider supports the top_k sampling parameter
	// (Anthropic, Bedrock; not OpenAI).
	TopK *bool `yaml:"top_k"`
	// TopP is true when the provider supports the top_p sampling parameter.
	TopP *bool `yaml:"top_p"`
	// FrequencyPenalty is true when the provider supports frequency_penalty.
	FrequencyPenalty *bool `yaml:"frequency_penalty"`
	// PresencePenalty is true when the provider supports presence_penalty.
	PresencePenalty *bool `yaml:"presence_penalty"`
	// Batch is true when the provider supports asynchronous batch inference.
	Batch *bool `yaml:"batch"`
	// ResponseFormat is true when the adapter honours the ResponseFormat field.
	ResponseFormat *bool `yaml:"response_format"`
	// ReasoningStyle is the reasoning effort configuration style:
	// "" | "none" | "effort_string" | "token_budget" | "both". Empty string
	// means "row doesn't override" — inherit the provider-level default.
	ReasoningStyle string `yaml:"reasoning_style"`

	// Attachment limits (multimodal-io-01KQ8TDF FR-005).
	// 0 / nil / empty means "use provider default".
	ImageInput              bool     `yaml:"image_input"`
	DocumentInput           bool     `yaml:"document_input"`
	MaxImageBytes           int64    `yaml:"max_image_bytes"`
	MaxDocumentBytes        int64    `yaml:"max_document_bytes"`
	MaxImageCountPerMessage int      `yaml:"max_image_count_per_message"`
	MaxImagePixels          int64    `yaml:"max_image_pixels"`
	MaxDocumentPages        int      `yaml:"max_document_pages"`
	ImageInputMimeTypes     []string `yaml:"image_input_mime_types"`
	DocumentInputMimeTypes  []string `yaml:"document_input_mime_types"`
}

// AttachmentDescriptor carries the resolved per-provider attachment limits
// returned by Catalog.AttachmentLimits. Zero values mean "unknown/unbounded".
type AttachmentDescriptor struct {
	ImageInput              bool
	DocumentInput           bool
	MaxImageBytes           int64
	MaxDocumentBytes        int64
	MaxImageCountPerMessage int
	MaxImagePixels          int64
	MaxDocumentPages        int
	ImageInputMimeTypes     []string
	DocumentInputMimeTypes  []string
}

// Catalog holds the loaded per-provider data and answers per-(provider,
// model) descriptor lookups.
type Catalog struct {
	specs map[string]*providerSpec
}

// providerAlias maps a provider kind with no YAML of its own onto the
// catalog entry it should resolve against (model-settings-reach-the-
// model-01PMZ101 WP02, spec D-1). Azure OpenAI's Chat Completions wire
// shape is identical to OpenAI's — the adapter already queries
// "openai" for its own Capabilities() shim
// (core/llm/azure/adapter.go:116) — so the catalog borrows the same
// descriptor instead of falling into the unknown-provider branch,
// which previously left azure-openai unable to advertise tool_calling
// at all (the P0: every tool-bearing request refused before any wire
// call).
//
// Applied in all three lookup entry points (Describe, DescribeRich,
// AttachmentLimits) — aliasing only one reproduces a half-fix, e.g.
// tools would pass while every image attachment kept refusing.
var providerAlias = map[string]string{
	"azure-openai": "openai",
}

// resolveProvider returns the catalog key to look up for provider,
// following providerAlias when provider has no dedicated YAML.
func resolveProvider(provider string) string {
	if alias, ok := providerAlias[provider]; ok {
		return alias
	}
	return provider
}

// LoadDefault loads the embedded YAML data shipped with the connector.
func LoadDefault() (*Catalog, error) {
	return Load(dataFS, "data")
}

// Load reads YAML files at root inside fsys and returns a Catalog.
func Load(fsys fs.FS, root string) (*Catalog, error) {
	c := &Catalog{specs: map[string]*providerSpec{}}
	entries, err := fs.ReadDir(fsys, root)
	if err != nil {
		return nil, fmt.Errorf("capabilities: read dir: %w", err)
	}
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		if !strings.HasSuffix(ent.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(root, ent.Name())
		raw, rerr := fs.ReadFile(fsys, path)
		if rerr != nil {
			return nil, fmt.Errorf("capabilities: read %s: %w", path, rerr)
		}
		var spec providerSpec
		if uerr := yaml.Unmarshal(raw, &spec); uerr != nil {
			return nil, fmt.Errorf("capabilities: parse %s: %w", path, uerr)
		}
		if spec.Provider == "" {
			return nil, fmt.Errorf("capabilities: %s: missing 'provider' field", path)
		}
		c.specs[spec.Provider] = &spec
	}
	return c, nil
}

// Describe returns the descriptor for (provider, model).
//
// If no entry matches the model, the descriptor is filled from the
// provider defaults and the Notes field carries an "unknown_model"
// breadcrumb (plan §R4 fallback: streaming-only safe baseline).
func (c *Catalog) Describe(provider, model string) llm.CapabilityDescriptor {
	desc := llm.CapabilityDescriptor{
		Provider: provider, Model: model,
		Supported: map[llm.Capability]bool{},
		Notes:     map[llm.Capability]string{},
	}
	spec, ok := c.specs[resolveProvider(provider)]
	if !ok {
		// Unknown provider entirely: streaming-only safe baseline so
		// that a brand-new adapter still has a usable default.
		desc.Supported[llm.CapStreaming] = true
		desc.Supported[llm.CapUsageReporting] = true
		desc.Notes[llm.CapStreaming] = "unknown_provider_default"
		return desc
	}
	// Start from defaults.
	applyDefaults(&desc, spec.Defaults)
	// Apply the first model glob that matches. A nil field on the matched
	// row means "row is silent on this capability" — keep whatever
	// applyDefaults already populated from the provider-level defaults
	// rather than zeroing it out (this mirrors applyRichEntry's merge
	// behaviour below; see WP08).
	setIfPresent := func(cap llm.Capability, v *bool) {
		if v != nil {
			desc.Supported[cap] = *v
		}
	}
	for _, m := range spec.Models {
		if matchGlob(m.Match, model) {
			setIfPresent(llm.CapStreaming, m.Streaming)
			setIfPresent(llm.CapToolCalling, m.ToolCalling)
			setIfPresent(llm.CapVision, m.Vision)
			setIfPresent(llm.CapDocuments, m.Documents)
			setIfPresent(llm.CapJSONMode, m.JSONMode)
			setIfPresent(llm.CapReasoning, m.Reasoning)
			setIfPresent(llm.CapCancellation, m.Cancellation)
			setIfPresent(llm.CapUsageReporting, m.UsageReporting)
			// Structured-output capability flags (structured-output-and-grammar-01KX5R8A FR-002).
			setIfPresent(llm.CapStructuredOutput, m.StructuredOutput)
			setIfPresent(llm.CapGrammar, m.Grammar)
			setIfPresent(llm.CapRegexGrammar, m.RegexGrammar)
			// Image output capability flag (multimodal-io-extended-01KQ8TD2 WP05).
			setIfPresent(llm.CapImageOutput, m.ImageOutput)
			return desc
		}
	}
	// Unknown model under a known provider: keep defaults but mark.
	desc.Notes[llm.CapStreaming] = "unknown_model_default"
	return desc
}

// parseReasoningStyle converts the YAML reasoning_style string to
// llm.ReasoningStyle. Unknown/empty values map to ReasoningNone.
func parseReasoningStyle(s string) llm.ReasoningStyle {
	switch s {
	case "effort_string":
		return llm.ReasoningEffortString
	case "token_budget":
		return llm.ReasoningTokenBudget
	case "both":
		return llm.ReasoningBoth
	default:
		return llm.ReasoningNone
	}
}

// DescribeRich returns the richer ProviderCapabilities record for
// (provider, model) (FR-001 + FR-020 of
// provider-implementation-uniformity-01KQ8V4F WP06).
//
// The returned record is built from the curated YAML data and is
// the synchronous floor returned by adapters when the cache misses.
// A lazy /models merge may add flags ADDITIVELY on top (false-flag in
// /models does NOT downgrade a curated true).
func (c *Catalog) DescribeRich(provider, model string) llm.ProviderCapabilities {
	caps := llm.ProviderCapabilities{
		Provider: provider,
		Model:    model,
	}
	spec, ok := c.specs[resolveProvider(provider)]
	if !ok {
		// Unknown provider: streaming-only safe baseline.
		caps.Streaming = true
		caps.UsageReporting = true
		caps.Notes = map[string]string{"streaming": "unknown_provider_default"}
		return caps
	}

	// Apply defaults from the YAML defaults map.
	applyRichDefaults(&caps, spec.Defaults)

	// Apply the first model glob that matches.
	for _, m := range spec.Models {
		if matchGlob(m.Match, model) {
			applyRichEntry(&caps, &m)
			return caps
		}
	}

	// Unknown model under a known provider: keep defaults but mark.
	if caps.Notes == nil {
		caps.Notes = map[string]string{}
	}
	caps.Notes["streaming"] = "unknown_model_default"
	return caps
}

// applyRichDefaults reads boolean capability defaults from the YAML
// defaults map (mixed bool/numeric map[string]any) into a
// ProviderCapabilities.
func applyRichDefaults(caps *llm.ProviderCapabilities, defaults map[string]any) {
	boolField := func(key string) bool {
		v, _ := defaults[key].(bool)
		return v
	}
	caps.Streaming = boolField("streaming")
	caps.ToolCalling = boolField("tool_calling")
	caps.Vision = boolField("vision")
	caps.Documents = boolField("documents")
	caps.JSONMode = boolField("json_mode")
	caps.Reasoning = boolField("reasoning")
	caps.Cancellation = boolField("cancellation")
	caps.UsageReporting = boolField("usage_reporting")
	caps.StructuredOutput = boolField("structured_output")
	caps.Grammar = boolField("grammar")
	caps.RegexGrammar = boolField("regex_grammar")
	caps.ImageOutput = boolField("image_output")
	caps.ParallelToolCalls = boolField("parallel_tool_calls")
	caps.Seed = boolField("seed")
	caps.Logprobs = boolField("logprobs")
	caps.TopK = boolField("top_k")
	caps.TopP = boolField("top_p")
	caps.FrequencyPenalty = boolField("frequency_penalty")
	caps.PresencePenalty = boolField("presence_penalty")
	caps.Batch = boolField("batch")
	caps.ResponseFormat = boolField("response_format")
	if s, ok := defaults["reasoning_style"].(string); ok {
		caps.Reasoning_ = parseReasoningStyle(s)
	}
}

// applyRichEntry overlays a matched modelEntry's explicitly-set capability
// flags onto caps, which must already carry the provider-level defaults
// (via applyRichDefaults). A nil flag means the model row is silent on
// that capability and caps keeps whatever the provider defaults gave it —
// this is the merge that was missing before WP08 (model-request-path-live-
// 01PMDL01): the old wholesale-overwrite copied every field's Go zero
// value (false) for any knob a model row didn't mention, defeating the
// provider-level defaults regardless of what they declared.
func applyRichEntry(caps *llm.ProviderCapabilities, m *modelEntry) {
	setBool := func(dst *bool, v *bool) {
		if v != nil {
			*dst = *v
		}
	}
	setBool(&caps.Streaming, m.Streaming)
	setBool(&caps.ToolCalling, m.ToolCalling)
	setBool(&caps.Vision, m.Vision)
	setBool(&caps.Documents, m.Documents)
	setBool(&caps.JSONMode, m.JSONMode)
	setBool(&caps.Reasoning, m.Reasoning)
	setBool(&caps.Cancellation, m.Cancellation)
	setBool(&caps.UsageReporting, m.UsageReporting)
	setBool(&caps.StructuredOutput, m.StructuredOutput)
	setBool(&caps.Grammar, m.Grammar)
	setBool(&caps.RegexGrammar, m.RegexGrammar)
	setBool(&caps.ImageOutput, m.ImageOutput)
	setBool(&caps.ParallelToolCalls, m.ParallelToolCalls)
	setBool(&caps.Seed, m.Seed)
	setBool(&caps.Logprobs, m.Logprobs)
	setBool(&caps.TopK, m.TopK)
	setBool(&caps.TopP, m.TopP)
	setBool(&caps.FrequencyPenalty, m.FrequencyPenalty)
	setBool(&caps.PresencePenalty, m.PresencePenalty)
	setBool(&caps.Batch, m.Batch)
	setBool(&caps.ResponseFormat, m.ResponseFormat)

	// ContextWindow / MaxOutputTokens: no provider-level default concept
	// exists today (applyRichDefaults never sets them), so a nonzero-only
	// overlay is equivalent to the old unconditional copy but is also
	// forward-safe should a defaults-level value ever be introduced.
	if m.ContextWindow != 0 {
		caps.ContextWindow = m.ContextWindow
	}
	if m.MaxOutputTokens != 0 {
		caps.MaxOutputTokens = m.MaxOutputTokens
	}

	// ReasoningStyle: empty means "row doesn't override" — keep whatever
	// applyRichDefaults resolved from the provider's reasoning_style default.
	if m.ReasoningStyle != "" {
		caps.Reasoning_ = parseReasoningStyle(m.ReasoningStyle)
	}
}

// ContextWindow returns the curated max context length in tokens for
// (provider, model). Returns 0 when no entry matches (caller should
// treat 0 as "unknown").
func (c *Catalog) ContextWindow(provider, model string) int {
	spec, ok := c.specs[provider]
	if !ok {
		return 0
	}
	for _, m := range spec.Models {
		if matchGlob(m.Match, model) {
			return m.ContextWindow
		}
	}
	return 0
}

// MaxOutputTokens returns the curated per-turn output token cap for
// (provider, model). Returns 0 when no entry matches or the field
// was not populated in the YAML — callers treat 0 as "unknown"
// (backend-context-window-length-01KQ8TD3 WP01).
func (c *Catalog) MaxOutputTokens(provider, model string) int {
	spec, ok := c.specs[provider]
	if !ok {
		return 0
	}
	for _, m := range spec.Models {
		if matchGlob(m.Match, model) {
			return m.MaxOutputTokens
		}
	}
	return 0
}

// Tier returns the curated size-tier ("small" | "medium" | "large") for
// (provider, model): the first glob match in the provider's tiers:
// table, falling back to the provider's `defaults: tier:` value. ok is
// false only when the provider has no tier data at all (neither a
// matching row nor a default), so callers can apply their own final
// fallback (agentgraph.BranchRecommender defaults to "medium").
//
// This is the provider-owned replacement for the frozen-core
// tierFromModelID name-matching that used to live in core/agentgraph
// (versioned-model-profile-01PMDL04 WP04): the core asks a data
// question through this method instead of string-matching model
// families itself.
func (c *Catalog) Tier(provider, model string) (string, bool) {
	spec, ok := c.specs[provider]
	if !ok {
		return "", false
	}
	for _, t := range spec.Tiers {
		if matchGlob(t.Match, model) {
			return t.Tier, true
		}
	}
	if t, ok := spec.Defaults["tier"].(string); ok && t != "" {
		return t, true
	}
	return "", false
}

// TierAny resolves a tier for model without knowing which provider it
// belongs to, scanning every loaded provider's tiers: table (in
// alphabetical provider order, for determinism) for a glob match. It
// exists so a caller with an unrecognised or custom provider kind
// (e.g. a "personal" provider naming a model after a known family, like
// "my-claude-haiku-mirror") can still get a tier opinion, without doing
// the family-name string-matching itself. Provider-level defaults are
// intentionally not consulted here (there is no single provider to pull
// a default from); ok is false when no provider's tiers: table matches.
func (c *Catalog) TierAny(model string) (string, bool) {
	names := make([]string, 0, len(c.specs))
	for name := range c.specs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, t := range c.specs[name].Tiers {
			if matchGlob(t.Match, model) {
				return t.Tier, true
			}
		}
	}
	return "", false
}

// KnownModel is one concrete (non-glob) entry from a provider's tiers:
// table, suitable for direct enumeration by callers that need real
// candidate model IDs rather than just a match pattern — e.g. the v1
// branch-recommender stopgap wiring (core/rpc/branches_wiring.go),
// pending the follow-up ListProviders hydration its own comment tracks.
type KnownModel struct {
	ModelID string
	Tier    string
}

// KnownModels returns every exact-match (non-wildcard) row in the
// provider's tiers: table, in file order. Glob rows ("claude-haiku-*")
// are skipped since they classify a model rather than name one.
func (c *Catalog) KnownModels(provider string) []KnownModel {
	spec, ok := c.specs[provider]
	if !ok {
		return nil
	}
	out := make([]KnownModel, 0, len(spec.Tiers))
	for _, t := range spec.Tiers {
		if strings.Contains(t.Match, "*") {
			continue
		}
		out = append(out, KnownModel{ModelID: t.Match, Tier: t.Tier})
	}
	return out
}

// AttachmentLimits returns the resolved attachment capability descriptor for
// (provider, model). The descriptor drives the capability gate
// (CheckAttachments) and the frontend's per-model tray caps.
// Zero values mean "unknown/unbounded" — callers should treat them as
// "skip this check" rather than "allow unlimited" for safety (multimodal-io-01KQ8TDF FR-007).
func (c *Catalog) AttachmentLimits(provider, model string) AttachmentDescriptor {
	// Default to restrictive: no image, no document.
	out := AttachmentDescriptor{
		ImageInputMimeTypes:    []string{"image/png", "image/jpeg", "image/gif", "image/webp"},
		DocumentInputMimeTypes: []string{"application/pdf"},
	}
	spec, ok := c.specs[resolveProvider(provider)]
	if !ok {
		return out
	}
	// Apply provider-level defaults from the YAML defaults map.
	applyAttachmentDefaults(&out, spec.Defaults)
	// Apply the first model glob that matches.
	for _, m := range spec.Models {
		if matchGlob(m.Match, model) {
			applyAttachmentEntry(&out, &m)
			break
		}
	}
	// FR-022: when HARNESS_MULTIMODAL_IN=off, force both input flags to
	// false regardless of per-provider YAML values. The gate (CheckAttachments)
	// and the frontend (ChatInput) both read these fields; setting them
	// to false here is the single source of truth for the env override.
	if !MultimodalInEnabled() {
		out.ImageInput = false
		out.DocumentInput = false
	}
	return out
}

// applyAttachmentDefaults reads attachment keys from the raw defaults map
// (which is map[string]any because it mixes bool and numeric values).
func applyAttachmentDefaults(out *AttachmentDescriptor, defaults map[string]any) {
	if v, ok := defaults["image_input"].(bool); ok {
		out.ImageInput = v
	}
	if v, ok := defaults["document_input"].(bool); ok {
		out.DocumentInput = v
	}
	if v, ok := intFromAny(defaults["max_image_bytes"]); ok {
		out.MaxImageBytes = v
	}
	if v, ok := intFromAny(defaults["max_document_bytes"]); ok {
		out.MaxDocumentBytes = v
	}
	if v, ok := intFromAny(defaults["max_image_count_per_message"]); ok {
		out.MaxImageCountPerMessage = int(v)
	}
	if v, ok := intFromAny(defaults["max_image_pixels"]); ok {
		out.MaxImagePixels = v
	}
	if v, ok := intFromAny(defaults["max_document_pages"]); ok {
		out.MaxDocumentPages = int(v)
	}
	if v, ok := defaults["image_input_mime_types"].([]any); ok {
		out.ImageInputMimeTypes = anySliceToStrings(v)
	}
	if v, ok := defaults["document_input_mime_types"].([]any); ok {
		out.DocumentInputMimeTypes = anySliceToStrings(v)
	}
}

// applyAttachmentEntry overlays a modelEntry's attachment fields onto out.
// Only non-zero fields win so that a partially-specified model row doesn't
// accidentally zero out a provider default.
func applyAttachmentEntry(out *AttachmentDescriptor, m *modelEntry) {
	// Boolean fields: always apply (false is a valid intentional value in a model row).
	out.ImageInput = m.ImageInput
	out.DocumentInput = m.DocumentInput
	if m.MaxImageBytes > 0 {
		out.MaxImageBytes = m.MaxImageBytes
	}
	if m.MaxDocumentBytes > 0 {
		out.MaxDocumentBytes = m.MaxDocumentBytes
	}
	if m.MaxImageCountPerMessage > 0 {
		out.MaxImageCountPerMessage = m.MaxImageCountPerMessage
	}
	if m.MaxImagePixels > 0 {
		out.MaxImagePixels = m.MaxImagePixels
	}
	if m.MaxDocumentPages > 0 {
		out.MaxDocumentPages = m.MaxDocumentPages
	}
	if len(m.ImageInputMimeTypes) > 0 {
		out.ImageInputMimeTypes = m.ImageInputMimeTypes
	}
	if len(m.DocumentInputMimeTypes) > 0 {
		out.DocumentInputMimeTypes = m.DocumentInputMimeTypes
	}
}

// intFromAny coerces YAML integer values (int / int64 / float64) to int64.
func intFromAny(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		return int64(n), true
	}
	return 0, false
}

// anySliceToStrings converts []any (YAML sequence) to []string.
func anySliceToStrings(in []any) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func applyDefaults(desc *llm.CapabilityDescriptor, defaults map[string]any) {
	boolKeymap := map[string]llm.Capability{
		"streaming":       llm.CapStreaming,
		"tool_calling":    llm.CapToolCalling,
		"vision":          llm.CapVision,
		"documents":       llm.CapDocuments,
		"json_mode":       llm.CapJSONMode,
		"reasoning":       llm.CapReasoning,
		"cancellation":    llm.CapCancellation,
		"usage_reporting": llm.CapUsageReporting,
		// Structured-output capability keys (structured-output-and-grammar-01KX5R8A FR-002).
		"structured_output": llm.CapStructuredOutput,
		"grammar":           llm.CapGrammar,
		"regex_grammar":     llm.CapRegexGrammar,
		// Image output capability key (multimodal-io-extended-01KQ8TD2 WP05).
		"image_output": llm.CapImageOutput,
	}
	for k, v := range defaults {
		if b, ok := v.(bool); ok {
			if cap, ok := boolKeymap[k]; ok {
				desc.Supported[cap] = b
			}
		}
	}
}

// MultimodalInEnabled reports whether the HARNESS_MULTIMODAL_IN env flag
// permits image and document input. Default on: the env var must be
// explicitly set to "off" (case-insensitive) to disable multimodal input.
// When disabled, AttachmentLimits forces ImageInput=false and
// DocumentInput=false regardless of the per-model YAML descriptors.
// (multimodal-io-01KQ8TDF WP08 / FR-022)
func MultimodalInEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("HARNESS_MULTIMODAL_IN")))
	return v != "off"
}

// MultimodalOutEnabled reports whether the HARNESS_MULTIMODAL_OUT env flag
// permits the model-generated image output pipeline. Default on: the env var
// must be explicitly set to "off" (case-insensitive) to disable. When
// disabled, the auto-capture pipeline skips all StreamGeneratedImage events
// regardless of the Settings.AutoCaptureGeneratedImages dial.
// (multimodal-io-extended-01KQ8TD2 WP08)
func MultimodalOutEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("HARNESS_MULTIMODAL_OUT")))
	return v != "off"
}

// matchGlob is a tiny prefix/suffix glob, sufficient for the connector's
// match expressions of shape "claude-sonnet-*" / "anthropic.claude-*".
// Only "*" is recognized as a wildcard, and only as a trailing or
// leading suffix.
func matchGlob(pattern, s string) bool {
	if pattern == "" {
		return false
	}
	if pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(s, prefix)
	}
	if strings.HasPrefix(pattern, "*") {
		suffix := strings.TrimPrefix(pattern, "*")
		return strings.HasSuffix(s, suffix)
	}
	return pattern == s
}
