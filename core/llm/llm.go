package llm

import (
	"context"
	"encoding/json"
	"strings"
)

// Capability identifies a model capability surfaced through the connector.
//
// Capability names are part of the connector's stable public contract:
// adapters report which capabilities they support per-(provider, model)
// via CapabilityDescriptor and the CapabilityGate (capabilities package)
// rejects requests for unsupported capabilities before any wire call
// (FR-013).
type Capability string

const (
	CapStreaming        Capability = "streaming"
	CapToolCalling      Capability = "tool_calling"
	CapVision           Capability = "vision"
	CapDocuments        Capability = "documents"
	CapJSONMode         Capability = "json_mode"
	CapPromptCaching    Capability = "prompt_caching"
	CapReasoning        Capability = "reasoning"
	CapCancellation     Capability = "cancellation"
	CapUsageReporting   Capability = "usage_reporting"
	// CapStructuredOutput reports that the model/provider natively supports
	// JSON-schema-constrained output (response_format or tool-call workaround
	// with validated extraction). When false, Mode="json_schema" falls back
	// to json_mode + post-hoc validation. (structured-output-and-grammar-01KX5R8A FR-002)
	CapStructuredOutput Capability = "structured_output"
	// CapGrammar reports that the model/runtime supports token-level GBNF
	// grammar constraints. Cloud providers return false; local runtimes
	// (llama.cpp via Ollama) return true. Mode="grammar" returns
	// ErrUnsupportedFormat when this is false. (structured-output-and-grammar-01KX5R8A FR-005)
	CapGrammar          Capability = "grammar"
	// CapRegexGrammar reports that the model/runtime supports regex-shorthand
	// grammar constraints (subset of full GBNF). (structured-output-and-grammar-01KX5R8A FR-002)
	CapRegexGrammar     Capability = "regex_grammar"
	// CapImageOutput reports that the model/provider supports generating
	// images as output (e.g. DALL-E 3, gpt-image-1, Titan Image).
	// Requests that populate GenerationRequest.ImageOutput or are routed to
	// an image-generation endpoint require this capability.
	// (multimodal-io-extended-01KQ8TD2 WP01)
	CapImageOutput      Capability = "image_output"
)

// AllCapabilities is the canonical ordered list (used for tests / docs).
func AllCapabilities() []Capability {
	return []Capability{
		CapStreaming, CapToolCalling, CapVision, CapDocuments, CapJSONMode,
		CapPromptCaching, CapReasoning, CapCancellation, CapUsageReporting,
		CapStructuredOutput, CapGrammar, CapRegexGrammar, CapImageOutput,
	}
}

// CapabilityDescriptor is the per-(provider, model) capability record.
//
// Descriptors live as YAML data (capabilities/data/<provider>.yaml) per
// plan §R4 — a code-only descriptor would drift faster than the data.
type CapabilityDescriptor struct {
	Provider  string                `json:"provider"`
	Model     string                `json:"model"`
	Supported map[Capability]bool   `json:"supported"`
	Notes     map[Capability]string `json:"notes,omitempty"`
}

// Has reports whether the descriptor advertises support for cap.
func (d CapabilityDescriptor) Has(cap Capability) bool {
	if d.Supported == nil {
		return false
	}
	return d.Supported[cap]
}

// CredentialReference is the indirect pointer that resolves to credential
// material at request time (FR-003 / C-002 / secrets-keychain FR-001).
//
// Plaintext credential bytes never enter this struct or any payload that
// passes through the connector — only the kind + locator (env var name,
// keychain entry id, file path, AWS profile name, KMS arn) ever appear.
type CredentialReference struct {
	// Kind is one of: env | keychain | file | aws_profile | kms.
	Kind string `json:"kind" yaml:"kind"`
	// Locator is the kind-specific lookup key.
	Locator string `json:"locator" yaml:"locator"`
}

// String returns a human-readable, redaction-safe rendering used in error
// messages and audit payloads. The locator is included because it is by
// definition the *reference* not the secret value.
func (r CredentialReference) String() string {
	if r.Kind == "" && r.Locator == "" {
		return "<unset>"
	}
	return r.Kind + ":" + r.Locator
}

// RetryPolicy controls the retry middleware (FR-016).
type RetryPolicy struct {
	MaxAttempts int    `json:"max_attempts" yaml:"max_attempts"`
	BaseMS      int    `json:"base_ms"      yaml:"base_ms"`
	MaxMS       int    `json:"max_ms"       yaml:"max_ms"`
	Jitter      string `json:"jitter"       yaml:"jitter"` // "full" | "none"
}

// DefaultRetryPolicy materializes spec OQ-3 / plan §9 default budget.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{MaxAttempts: 3, BaseMS: 250, MaxMS: 5000, Jitter: "full"}
}

// ProviderProfile is the materialized form of one bundle Provider Profile
// artifact (FR-002 / C-004).
type ProviderProfile struct {
	ID    string              `json:"id"               yaml:"id"`
	Kind  string              `json:"kind"             yaml:"kind"`
	Model string              `json:"model"            yaml:"model"`
	// Models is the full set of models the credential is authorised
	// for. When empty, callers fall back to [Model]. The first entry is
	// the default; the chat surface picks via GenerationRequest.Model.
	Models          []string            `json:"models,omitempty" yaml:"models,omitempty"`
	Cred            CredentialReference `json:"cred"             yaml:"auth"`
	Region          string              `json:"region,omitempty" yaml:"region,omitempty"`
	Endpoint        string              `json:"endpoint,omitempty" yaml:"endpoint,omitempty"`
	CapabilityHints map[Capability]bool `json:"capability_hints,omitempty" yaml:"capabilities,omitempty"`
	Defaults        map[string]any      `json:"defaults,omitempty" yaml:"defaults,omitempty"`
	Retry           *RetryPolicy        `json:"retry,omitempty"  yaml:"retry,omitempty"`
}

// AvailableModels returns the resolved model list. Falls back to
// [Model] when Models is empty so legacy single-model profiles keep
// working without a migration.
func (p ProviderProfile) AvailableModels() []string {
	if len(p.Models) > 0 {
		return p.Models
	}
	if p.Model != "" {
		return []string{p.Model}
	}
	return nil
}

// Role enumerates the message roles in a multi-turn conversation (FR-005).
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is one entry in the conversation history.
type Message struct {
	Role    Role           `json:"role"`
	Content []ContentBlock `json:"content"`
}

// NewTextMessage builds the common case — a message with a single text
// block. Most call sites of the connector wrap a plain string into a
// Message; this helper is the canonical constructor (FR-002).
func NewTextMessage(role Role, text string) Message {
	return Message{
		Role:    role,
		Content: []ContentBlock{{Type: "text", Text: text}},
	}
}

// Text flattens all text-typed blocks in declaration order, joining
// them with a blank-line separator. Returns "" when no text blocks
// exist (all-image / all-document messages). Used by legacy callers
// that haven't migrated to block-aware logic (FR-002).
func (m Message) Text() string {
	var b strings.Builder
	for _, c := range m.Content {
		if c.Type != "text" && c.Type != "" {
			continue
		}
		if c.Text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(c.Text)
	}
	return b.String()
}

// ContentBlock is a polymorphic content fragment carrying text, an
// image / document media reference, or a tool call / result. The Type
// field selects which sibling field is populated:
//
//   - "text"            → Text
//   - "image"           → Source (MediaType image/png|jpeg|gif|webp)
//   - "document"        → Source (MediaType application/pdf|csv|html|txt|md)
//   - "tool_use"        → ToolUse
//   - "tool_result"     → ToolResult OR ToolData (legacy raw-bytes shape)
//   - "generated_image" → ArtifactID (captured model-generated image;
//                         bytes resolved via harness-artifact://<ArtifactID>)
//
// (multimodal-io-extended-01KQ8TD2 WP01)
type ContentBlock struct {
	Type       string          `json:"type"`
	Text       string          `json:"text,omitempty"`
	Source     *MediaSource    `json:"source,omitempty"`
	ToolUse    *ToolUse        `json:"tool_use,omitempty"`
	ToolResult *ToolResult     `json:"tool_result,omitempty"`
	ToolData   json.RawMessage `json:"tool_data,omitempty"`
	// ArtifactID is set when Type=="generated_image". It holds the ULID of
	// the captured artifact row; the frontend resolves bytes via
	// harness-artifact://<ArtifactID>. Empty when the image has not yet
	// been captured (inline Source.Data path before WP02 auto-capture runs).
	ArtifactID string `json:"artifact_id,omitempty"`
}

// ImageDimensions carries the pixel dimensions of an image attachment.
// Populated by core/attachments on ingest and by the frontend before
// calling Attachments_AddMedia. The capability gate uses these values
// to enforce per-provider MaxImagePixels caps (multimodal-io-01KQ8TDF FR-001).
type ImageDimensions struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// Pixels returns the total pixel count (Width × Height). Returns 0
// when either dimension is zero.
func (d *ImageDimensions) Pixels() int64 {
	if d == nil || d.Width <= 0 || d.Height <= 0 {
		return 0
	}
	return int64(d.Width) * int64(d.Height)
}

// MediaSource carries the bytes (or future URI reference) for an image
// or document content block. Kind discriminates the storage form;
// "base64" is the canonical inline shape and "uri" is reserved for a
// future remote-fetch path.
//
// SizeBytes and ImageDimensions are populated by core/attachments on
// ingest (multimodal-io-01KQ8TDF FR-001). The capability gate reads
// them to enforce per-provider byte and pixel caps without re-decoding
// the base64 payload.
type MediaSource struct {
	Kind         string           `json:"kind"`
	MediaType    string           `json:"media_type"`
	Data         string           `json:"data,omitempty"`
	URI          string           `json:"uri,omitempty"`
	OriginalName string           `json:"original_name,omitempty"`
	// SizeBytes is the decoded byte size of the attachment. Populated by
	// core/attachments.Put and by the frontend before Attachments_AddMedia.
	// Zero means unknown (skip byte-cap check in the gate).
	SizeBytes        int64            `json:"size_bytes,omitempty"`
	// ImageDimensions carries width × height in pixels for image blocks.
	// Nil means unknown. The gate skips the pixel-cap check when nil.
	ImageDimensions  *ImageDimensions `json:"image_dimensions,omitempty"`
}

// ToolUse is a model-emitted tool call (FR-006).
type ToolUse struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// ToolResult carries the typed payload returned by a tool invocation
// when surfaced as a content block. The legacy ContentBlock.ToolData
// raw-bytes encoding remains supported for callers that haven't yet
// migrated to the typed form.
type ToolResult struct {
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

// ToolSpec declares a callable tool the model may invoke (FR-006).
type ToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// Attachment carries non-text input for a message (vision images, audio,
// etc.; FR-007).
type Attachment struct {
	Kind     string `json:"kind"`           // "image" | "audio" | ...
	MIME     string `json:"mime,omitempty"` // image/png, image/jpeg, ...
	URI      string `json:"uri,omitempty"`  // file://, data:, https://...
	Data     []byte `json:"-"`              // inline bytes; never logged
	AltText  string `json:"alt_text,omitempty"`
}

// ResponseFormat constrains the model's output. Three modes:
//
//   - nil             — free-form text (today's behavior, FR-001)
//   - Mode="json"     — guarantee parseable JSON, no schema
//   - Mode="json_schema" — guarantee parseable JSON conforming to Schema
//   - Mode="grammar"  — token-level GBNF grammar constraint (local runtimes only)
//
// The adapter chooses the strongest backing it supports via the cascade
// documented in spec §2.1:
//
//   Mode=json_schema + CapStructuredOutput  → native schema pass-through
//   Mode=json_schema + CapJSONMode only     → JSON mode + post-hoc validate
//   Mode=json_schema + neither              → prompt-engineering + post-hoc validate
//   Mode=grammar     + CapGrammar           → native grammar
//   Mode=grammar     + !CapGrammar          → ErrUnsupportedFormat (no emulation)
//
// Callers never reason about the cascade; they receive either a valid
// response or a typed error (structured-output-and-grammar-01KX5R8A §2.1).
type ResponseFormat struct {
	// Mode is one of "json" | "json_schema" | "grammar".
	Mode string `json:"mode"`
	// Schema is the JSON Schema bytes. Populated when Mode == "json_schema".
	// The schema is validated by the adapter before the wire call. Nil is
	// treated as an open schema (any JSON object is valid).
	Schema json.RawMessage `json:"schema,omitempty"`
	// Grammar is the GBNF grammar bytes. Populated when Mode == "grammar".
	// Only honoured when the (provider, model) advertises CapGrammar.
	Grammar []byte `json:"grammar,omitempty"`
	// StrictValidation, when true, fails the call immediately with
	// ErrResponseValidationFailed if the response doesn't match the schema.
	// When false (default), validation failure triggers ONE auto-retry with a
	// corrective tail-prompt; second failure returns ErrResponseValidationFailed
	// with the raw response. Env var HARNESS_RESPONSE_FORMAT_NO_RETRY=1 suppresses
	// retries globally.
	StrictValidation bool `json:"strict_validation,omitempty"`
}

// StructuredOutputAdapter is the optional interface adapters implement when
// they support ResponseFormat natively. Adapters that do not implement this
// interface fall through to the prompt-engineering + post-hoc validation path.
//
// ApplyResponseFormat mutates wireBody in place to add provider-specific
// structured-output fields. It must return ErrUnsupportedFormat when the
// requested mode cannot be served (e.g. grammar on a cloud provider).
// (structured-output-and-grammar-01KX5R8A §2.4)
type StructuredOutputAdapter interface {
	ApplyResponseFormat(req *GenerationRequest, wireBody map[string]any) error
}

// JSONModeSpec opts into structured-output mode (FR-008).
//
// The Strict and Name fields extend the base shape for WP03 per-adapter
// translation (multimodal-io-extended-01KQ8TD2 WP01 / WP03):
//   - Strict=true   → propagates "strict: true" to OpenAI's json_schema
//                     response_format (additionalProperties: false injected).
//   - Name          → names the schema object in providers that require one
//                     (OpenAI json_schema.name). Falls back to "response" when empty.
type JSONModeSpec struct {
	Enabled bool            `json:"enabled"`
	Schema  json.RawMessage `json:"schema,omitempty"`
	// Strict, when true, enables strict schema validation mode on providers
	// that support it (OpenAI strict JSON schema). Ignored on providers without
	// strict-mode support. (multimodal-io-extended-01KQ8TD2 WP01)
	Strict bool   `json:"strict,omitempty"`
	// Name is the schema name forwarded to providers that require one.
	// Defaults to "response" when empty. (multimodal-io-extended-01KQ8TD2 WP01)
	Name   string `json:"name,omitempty"`
}

// CachingSpec is an opt-in marker for prompt-caching (FR-009).
type CachingSpec struct {
	Enabled bool   `json:"enabled"`
	// Breakpoints is a list of message indexes that mark cacheable prefixes.
	// Anthropic's `cache_control` semantics are the canonical exemplar.
	Breakpoints []int `json:"breakpoints,omitempty"`
}

// ReasoningSpec opts into extended-thinking output (FR-010).
type ReasoningSpec struct {
	Enabled        bool `json:"enabled"`
	BudgetTokens   int  `json:"budget_tokens,omitempty"`
	IncludeRawFrames bool `json:"include_raw_frames,omitempty"`
}

// ImageOutputSpec opts a request into image generation mode.
// When non-nil, the request targets an image-generation model/endpoint
// (e.g. DALL-E 3, gpt-image-1, Amazon Titan Image). The capability gate
// requires CapImageOutput. (multimodal-io-extended-01KQ8TD2 WP01)
type ImageOutputSpec struct {
	// N is the number of images to generate. Defaults to 1 when zero.
	N       int    `json:"n,omitempty"`
	// Size is the image dimensions as a provider-specific string
	// (e.g. "1024x1024", "1792x1024"). Empty means provider default.
	Size    string `json:"size,omitempty"`
	// Quality is the image quality level (e.g. "standard", "hd" for DALL-E 3;
	// "standard", "premium" for gpt-image-1). Empty means provider default.
	Quality string `json:"quality,omitempty"`
	// Style is a provider-specific style hint (e.g. "vivid", "natural" for
	// DALL-E 3). Empty means provider default.
	Style   string `json:"style,omitempty"`
}

// GenerationRequest is the provider-agnostic invocation shape (FR-004).
type GenerationRequest struct {
	ProfileID string `json:"profile_id"`
	// Model is an optional per-request override of ProviderProfile.Model.
	// When set and present in profile.AvailableModels(), the registry
	// substitutes prof.Model = req.Model before dispatching to the
	// adapter. Empty means "use the profile default."
	Model    string     `json:"model,omitempty"`
	System   string     `json:"system,omitempty"`
	Messages []Message  `json:"messages"`
	Tools    []ToolSpec `json:"tools,omitempty"`
	// Deprecated: use Message.Content blocks (ContentBlock with Type "image"
	// or "document") instead. The legacy Attachment slice is still honoured by
	// RequestedCapabilities and the registry's capability gate so existing
	// callers remain valid. (multimodal-io-01KQ8TDF FR-004)
	Attachments []Attachment   `json:"attachments,omitempty"`
	JSONMode    *JSONModeSpec  `json:"json_mode,omitempty"`
	Caching     *CachingSpec   `json:"caching,omitempty"`
	Reasoning   *ReasoningSpec `json:"reasoning,omitempty"`
	// ResponseFormat constrains the model's output to a JSON schema or GBNF
	// grammar. Nil means free-form text (today's behavior unchanged).
	// (structured-output-and-grammar-01KX5R8A FR-001)
	ResponseFormat *ResponseFormat  `json:"response_format,omitempty"`
	// ImageOutput opts the request into image-generation mode. Non-nil
	// triggers CapImageOutput capability gating. The adapter routes to the
	// image-generation endpoint rather than the chat-completions endpoint.
	// (multimodal-io-extended-01KQ8TD2 WP01)
	ImageOutput    *ImageOutputSpec `json:"image_output,omitempty"`
	Params         map[string]any   `json:"params,omitempty"`
	RetryOverride  *RetryPolicy     `json:"retry_override,omitempty"`
	SessionID      string           `json:"session_id,omitempty"`
	// Knobs carries typed per-request fine-tuning overrides (FR-017 of
	// provider-implementation-uniformity-01KQ8V4F). Nil means "inherit
	// from session / global / profile defaults." Individual sub-fields
	// are merged with the per-adapter defaults at build-request time.
	//
	// The wirecheck:"-" tag excludes this field from the
	// registry_completeness_test because Knobs is a composite that
	// serialises as flattened sub-fields on the wire — each sub-field
	// carries its own adapter-level coverage entry when they are tracked
	// in coverage_registry.yaml (WP08).
	Knobs          *RequestKnobs    `json:"knobs,omitempty" wirecheck:"-"`
}

// RequestedCapabilities returns the set of capabilities this request
// actually opts into. The capability gate (capabilities package) checks
// this set against the resolved descriptor.
func (r GenerationRequest) RequestedCapabilities() []Capability {
	caps := []Capability{CapStreaming} // every request is streaming
	if len(r.Tools) > 0 {
		caps = append(caps, CapToolCalling)
	}
	if len(r.Attachments) > 0 {
		caps = append(caps, CapVision)
	}
	if r.JSONMode != nil && r.JSONMode.Enabled {
		caps = append(caps, CapJSONMode)
	}
	if r.Caching != nil && r.Caching.Enabled {
		caps = append(caps, CapPromptCaching)
	}
	if r.Reasoning != nil && r.Reasoning.Enabled {
		caps = append(caps, CapReasoning)
	}
	// ResponseFormat capability gating (structured-output-and-grammar-01KX5R8A FR-002).
	// Mode="json_schema" requests CapStructuredOutput so the gate can
	// distinguish native support from the JSON-mode fallback path.
	// Mode="grammar" requests CapGrammar so the gate returns ErrUnsupportedFormat
	// for cloud providers that cannot emulate token-level constraints.
	if r.ResponseFormat != nil {
		switch r.ResponseFormat.Mode {
		case "json_schema":
			caps = append(caps, CapStructuredOutput)
		case "grammar":
			caps = append(caps, CapGrammar)
		}
	}
	// Image output capability gating (multimodal-io-extended-01KQ8TD2 WP01).
	if r.ImageOutput != nil {
		caps = append(caps, CapImageOutput)
	}
	return caps
}

// StreamEventKind classifies a streaming chunk.
type StreamEventKind string

const (
	StreamText           StreamEventKind = "text"
	StreamTool           StreamEventKind = "tool"
	StreamReasoning      StreamEventKind = "reasoning"
	StreamUsage          StreamEventKind = "usage"
	StreamFinish         StreamEventKind = "finish"
	StreamError          StreamEventKind = "error"
	// StreamGeneratedImage is emitted by image-generation adapters when the
	// model produces an image output. The event carries a GeneratedImagePayload
	// with the raw bytes (or a URL for providers that return a URL instead of
	// inline bytes). The llmnode auto-capture pipeline (WP02) captures the
	// bytes into the artifact store and rewrites the block's ArtifactID.
	// (multimodal-io-extended-01KQ8TD2 WP01)
	StreamGeneratedImage StreamEventKind = "generated_image"
)

// GeneratedImagePayload carries the payload for a StreamGeneratedImage event.
// Exactly one of Data or URL is non-empty; adapters that return inline base64
// populate Data; adapters that return a temporary URL (e.g. DALL-E 3 default)
// populate URL. (multimodal-io-extended-01KQ8TD2 WP01)
type GeneratedImagePayload struct {
	// Data holds the raw image bytes when the provider returns inline content
	// (e.g. b64_json format for DALL-E 3 / gpt-image-1).
	Data []byte `json:"data,omitempty"`
	// URL holds the temporary CDN URL when the provider returns a URL
	// instead of inline bytes (e.g. DALL-E 3 url format). The auto-capture
	// pipeline fetches and stores it within AutoCaptureGeneratedImages.
	URL string `json:"url,omitempty"`
	// MimeType is the IANA MIME type of the image (e.g. "image/png").
	// Derived from the provider's format hint or the fetched Content-Type.
	MimeType string `json:"mime_type,omitempty"`
	// RevisedPrompt is the provider-rewritten prompt (DALL-E 3 returns this).
	// Empty for providers that don't revise the prompt.
	RevisedPrompt string `json:"revised_prompt,omitempty"`
	// Index is the 0-based position within a multi-image response (n>1 requests).
	Index int `json:"index,omitempty"`
}

// StreamEvent is one delta delivered through a Stream.
type StreamEvent struct {
	Kind           StreamEventKind        `json:"kind"`
	Text           string                 `json:"text,omitempty"`
	Tool           *ToolUse               `json:"tool,omitempty"`
	Reasoning      *ReasoningBlock        `json:"reasoning,omitempty"`
	Usage          *Usage                 `json:"usage,omitempty"`
	Finish         string                 `json:"finish,omitempty"`
	Err            string                 `json:"err,omitempty"`
	Raw            json.RawMessage        `json:"raw,omitempty"`
	// GeneratedImage is set when Kind==StreamGeneratedImage.
	// (multimodal-io-extended-01KQ8TD2 WP01)
	GeneratedImage *GeneratedImagePayload `json:"generated_image,omitempty"`
}

// Stream is the streaming response surface (FR-004 / FR-012).
//
// Concrete implementations MUST honor context.Context cancellation AND
// also expose Cancel() so callers without a context handle can still
// terminate the upstream connection within ≤ 1 s p99 (NFR-003 / R5).
type Stream interface {
	// Events returns a receive channel of streaming chunks. The channel
	// is closed when the stream terminates (success or failure). Use
	// Final() to retrieve the terminal Response or error after the
	// channel closes.
	Events() <-chan StreamEvent
	// Cancel terminates the upstream connection. Safe to call from any
	// goroutine; safe to call after the stream has already finished.
	Cancel() error
	// Final returns the terminal Response. Blocks until the events
	// channel closes. After Cancel, Final returns ErrCancelled.
	Final() (Response, error)
}

// ContentBlockFromText is a small constructor used by adapters to emit
// text deltas without spelling out the full struct.
func ContentBlockFromText(text string) ContentBlock {
	return ContentBlock{Type: "text", Text: text}
}

// ReasoningBlock is one model reasoning frame (FR-010).
type ReasoningBlock struct {
	Type    string          `json:"type"`
	Content string          `json:"content"`
	Summary string          `json:"summary,omitempty"`
	Raw     json.RawMessage `json:"raw,omitempty"`
}

// Usage aggregates per-request token accounting (FR-011).
type Usage struct {
	InputTokens       int `json:"input_tokens"`
	OutputTokens      int `json:"output_tokens"`
	CachedInputRead   int `json:"cached_input_read,omitempty"`
	CachedInputWrite  int `json:"cached_input_write,omitempty"`
	ReasoningTokens   int `json:"reasoning_tokens,omitempty"`
	// ImagesGenerated is the count of images produced in the response.
	// Non-zero for image-generation requests (DALL-E 3, gpt-image-1, Titan Image).
	// (multimodal-io-extended-01KQ8TD2 WP01)
	ImagesGenerated   int `json:"images_generated,omitempty"`
}

// Cost is the derived currency cost for a Usage snapshot (FR-011).
type Cost struct {
	Currency      string  `json:"currency"`
	Total         float64 `json:"total"`
	InputCost     float64 `json:"input_cost,omitempty"`
	OutputCost    float64 `json:"output_cost,omitempty"`
	CachedCost    float64 `json:"cached_cost,omitempty"`
	ReasoningCost float64 `json:"reasoning_cost,omitempty"`
	// ImageCost is the dollar cost for images generated in this request.
	// Non-zero only when ImagesGenerated > 0 and a pricing entry was found
	// for the image model. (multimodal-io-extended-01KQ8TD2 WP01)
	ImageCost     float64 `json:"image_cost,omitempty"`
	// Indeterminate signals that no price-table entry matched the
	// (kind, model) pair — the request still completes (US3 Acceptance 1).
	Indeterminate bool   `json:"indeterminate,omitempty"`
	Source        string `json:"source,omitempty"`
}

// Response is the terminal result of a generation (FR-011).
type Response struct {
	Content      []ContentBlock   `json:"content"`
	ToolCalls    []ToolUse        `json:"tool_calls,omitempty"`
	Reasoning    []ReasoningBlock `json:"reasoning,omitempty"`
	FinishReason string           `json:"finish_reason"`
	Usage        Usage            `json:"usage"`
	Cost         Cost             `json:"cost"`
	Attempts     int              `json:"attempts"`
	SnapshotID   string           `json:"snapshot_id,omitempty"`
}

// ProviderAdapter is the per-provider plug-in contract (FR-018).
//
// Implementations live in core/llm/<provider>/ and may import that
// provider's SDK; no other package may import a provider SDK
// (DIRECTIVE_001 / C-001 / C-005). Adapters MUST classify provider
// errors into the typed taxonomy in errors.go so that the retry
// middleware can act without provider knowledge.
type ProviderAdapter interface {
	Kind() string
	Capabilities(model string) CapabilityDescriptor
	Stream(ctx context.Context, req GenerationRequest, prof ProviderProfile, cred []byte) (Stream, error)
}

// ModelInfo is one entry returned by a ModelLister-capable adapter. It
// carries enough metadata to populate the AddProvider model-picker
// dropdown without round-tripping back to the adapter.
type ModelInfo struct {
	ID            string `json:"id"`                          // canonical model id (e.g. "claude-sonnet-4-5")
	DisplayName   string `json:"display_name"`                // user-facing label (e.g. "Claude Sonnet 4.5")
	Description   string `json:"description,omitempty"`
	ContextWindow int    `json:"context_window,omitempty"`   // max input+output tokens; 0 = unknown
	// MaxOutputTokens is the provider's hard cap on tokens in a single
	// response. 0 means unknown — the UI should not render an explicit
	// cap in that case. Sourced from the capabilities catalog
	// (backend-context-window-length-01KQ8TD3 WP01).
	MaxOutputTokens int `json:"max_output_tokens,omitempty"` // max completion tokens per turn; 0 = unknown
}

// ModelLister is the optional capability adapters opt into when their
// provider exposes a /models endpoint. The rpc layer probes for this
// interface to drive the "auto-fill the model dropdown after the user
// pastes their key" flow. Adapters that do not implement it fall back
// to the static-capability path (user types the model id manually).
type ModelLister interface {
	ListModels(ctx context.Context, cred []byte) ([]ModelInfo, error)
}

// KeyTester is the opt-in capability for validating that a credential is
// accepted by the provider without modifying any stored state. Adapters
// MUST implement a 5-second context-bounded call to a low-cost endpoint
// (typically the /models endpoint reused from ModelLister).
//
// Returns nil if and only if the provider accepted the credential and returned
// a non-empty model list. On failure returns a typed error (*ErrAuth,
// *ErrTransient, or *ErrInvalidRequest) from the existing adapter taxonomy.
//
// The rotation pipeline in views/llm.API uses this interface before writing
// the new credential to the keychain (provider-keychain-rotation-01KQ8TD9 WP02).
type KeyTester interface {
	// TestKey verifies cred is accepted by the provider.
	// Implementations MUST apply a 5-second deadline internally.
	// Empty cred → *ErrAuth{Status:0, Message:"<provider>: empty credential"}.
	TestKey(ctx context.Context, cred []byte) error
}

// PreflightResult reports the outcome of one credential-resolution
// attempt at startup (FR-019).
type PreflightResult struct {
	ProfileID string `json:"profile_id"`
	Kind      string `json:"kind"`
	Resolved  bool   `json:"resolved"`
	Err       error  `json:"-"`
	Message   string `json:"message,omitempty"`
}

// Registry is the connector's public façade. All callers (rpc, session
// executor, test harness) reach the connector through this interface;
// no caller imports a provider package directly.
type Registry interface {
	// RegisterAdapter registers a ProviderAdapter for its declared
	// Kind(). The last registration wins; this is the OSS / enterprise
	// extensibility seam (FR-018 / C-005).
	RegisterAdapter(a ProviderAdapter)

	// LoadProfiles installs a set of materialized ProviderProfile values
	// in the registry. Profile ids must be unique; duplicates return an
	// error and the registry is left unchanged.
	LoadProfiles(profs []ProviderProfile) error

	// Profile looks up a profile by id.
	Profile(id string) (ProviderProfile, error)

	// PreflightAll resolves every loaded profile's credential reference
	// and reports the per-profile outcome (FR-019). It does not invoke
	// any model.
	PreflightAll(ctx context.Context) []PreflightResult

	// Stream dispatches a GenerationRequest through the full pipeline:
	// profile lookup → CapabilityGate → PolicyGuard → CredentialResolver
	// → AuditEmitter → RetryMiddleware → adapter → CostReducer.
	Stream(ctx context.Context, req GenerationRequest) (Stream, error)
}
