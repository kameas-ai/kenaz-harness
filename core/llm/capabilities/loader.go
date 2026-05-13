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
	"strings"

	"gopkg.in/yaml.v3"

	llm "github.com/sigil-tech/kaneaz-harness/core/llm"
)

//go:embed data/*.yaml
var dataFS embed.FS

// providerSpec is the on-disk schema (capabilities/data/<provider>.yaml).
type providerSpec struct {
	Provider string         `yaml:"provider"`
	Defaults map[string]any `yaml:"defaults"`
	Models   []modelEntry   `yaml:"models"`
}

// modelEntry is one per-model override row in a provider YAML.
// Boolean fields mirror the capability constants; integer/slice fields
// carry attachment limits (multimodal-io-01KQ8TDF FR-005).
type modelEntry struct {
	Match          string `yaml:"match"`
	Streaming      bool   `yaml:"streaming"`
	ToolCalling    bool   `yaml:"tool_calling"`
	Vision         bool   `yaml:"vision"`
	Documents      bool   `yaml:"documents"`
	JSONMode       bool   `yaml:"json_mode"`
	PromptCaching  bool   `yaml:"prompt_caching"`
	Reasoning      bool   `yaml:"reasoning"`
	Cancellation   bool   `yaml:"cancellation"`
	UsageReporting bool   `yaml:"usage_reporting"`
	// StructuredOutput is true when the model/provider supports native
	// JSON-schema-constrained output (response_format or tool-call workaround
	// with validated extraction). (structured-output-and-grammar-01KX5R8A FR-002)
	StructuredOutput bool `yaml:"structured_output"`
	// Grammar is true when the model/runtime supports token-level GBNF
	// grammar constraints. True only for local runtimes (llama.cpp / Ollama).
	// (structured-output-and-grammar-01KX5R8A FR-002)
	Grammar bool `yaml:"grammar"`
	// RegexGrammar is true when the model/runtime supports regex-shorthand
	// grammar constraints. (structured-output-and-grammar-01KX5R8A FR-002)
	RegexGrammar bool `yaml:"regex_grammar"`
	// ImageOutput is true when the model/provider supports generating images
	// as output (e.g. DALL-E 3, gpt-image-1, Amazon Titan Image).
	// (multimodal-io-extended-01KQ8TD2 WP05)
	ImageOutput bool `yaml:"image_output"`
	// ContextWindow is the model's max context length in tokens (0 = unknown).
	ContextWindow int `yaml:"context_window"`
	// MaxOutputTokens is the provider's hard cap on completion tokens per
	// turn (0 = unknown). Sourced from provider documentation; surfaced in
	// ModelInfo for the frontend context-window indicator
	// (backend-context-window-length-01KQ8TD3 WP01).
	MaxOutputTokens int `yaml:"max_output_tokens"`

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
	spec, ok := c.specs[provider]
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
	// Apply the first model glob that matches.
	for _, m := range spec.Models {
		if matchGlob(m.Match, model) {
			desc.Supported[llm.CapStreaming] = m.Streaming
			desc.Supported[llm.CapToolCalling] = m.ToolCalling
			desc.Supported[llm.CapVision] = m.Vision
			desc.Supported[llm.CapDocuments] = m.Documents
			desc.Supported[llm.CapJSONMode] = m.JSONMode
			desc.Supported[llm.CapPromptCaching] = m.PromptCaching
			desc.Supported[llm.CapReasoning] = m.Reasoning
			desc.Supported[llm.CapCancellation] = m.Cancellation
			desc.Supported[llm.CapUsageReporting] = m.UsageReporting
			// Structured-output capability flags (structured-output-and-grammar-01KX5R8A FR-002).
			desc.Supported[llm.CapStructuredOutput] = m.StructuredOutput
			desc.Supported[llm.CapGrammar] = m.Grammar
			desc.Supported[llm.CapRegexGrammar] = m.RegexGrammar
			// Image output capability flag (multimodal-io-extended-01KQ8TD2 WP05).
			desc.Supported[llm.CapImageOutput] = m.ImageOutput
			return desc
		}
	}
	// Unknown model under a known provider: keep defaults but mark.
	desc.Notes[llm.CapStreaming] = "unknown_model_default"
	return desc
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
	spec, ok := c.specs[provider]
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
		"streaming":        llm.CapStreaming,
		"tool_calling":     llm.CapToolCalling,
		"vision":           llm.CapVision,
		"documents":        llm.CapDocuments,
		"json_mode":        llm.CapJSONMode,
		"prompt_caching":   llm.CapPromptCaching,
		"reasoning":        llm.CapReasoning,
		"cancellation":     llm.CapCancellation,
		"usage_reporting":  llm.CapUsageReporting,
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
