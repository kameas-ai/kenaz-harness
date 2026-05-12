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
	CapStreaming      Capability = "streaming"
	CapToolCalling    Capability = "tool_calling"
	CapVision         Capability = "vision"
	CapDocuments      Capability = "documents"
	CapJSONMode       Capability = "json_mode"
	CapPromptCaching  Capability = "prompt_caching"
	CapReasoning      Capability = "reasoning"
	CapCancellation   Capability = "cancellation"
	CapUsageReporting Capability = "usage_reporting"
)

// AllCapabilities is the canonical ordered list (used for tests / docs).
func AllCapabilities() []Capability {
	return []Capability{
		CapStreaming, CapToolCalling, CapVision, CapDocuments, CapJSONMode,
		CapPromptCaching, CapReasoning, CapCancellation, CapUsageReporting,
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
//   - "text"        → Text
//   - "image"       → Source (MediaType image/png|jpeg|gif|webp)
//   - "document"    → Source (MediaType application/pdf|csv|html|txt|md)
//   - "tool_use"    → ToolUse
//   - "tool_result" → ToolResult OR ToolData (legacy raw-bytes shape)
type ContentBlock struct {
	Type       string          `json:"type"`
	Text       string          `json:"text,omitempty"`
	Source     *MediaSource    `json:"source,omitempty"`
	ToolUse    *ToolUse        `json:"tool_use,omitempty"`
	ToolResult *ToolResult     `json:"tool_result,omitempty"`
	ToolData   json.RawMessage `json:"tool_data,omitempty"`
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

// JSONModeSpec opts into structured-output mode (FR-008).
type JSONModeSpec struct {
	Enabled bool            `json:"enabled"`
	Schema  json.RawMessage `json:"schema,omitempty"`
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

// GenerationRequest is the provider-agnostic invocation shape (FR-004).
type GenerationRequest struct {
	ProfileID string `json:"profile_id"`
	// Model is an optional per-request override of ProviderProfile.Model.
	// When set and present in profile.AvailableModels(), the registry
	// substitutes prof.Model = req.Model before dispatching to the
	// adapter. Empty means "use the profile default."
	Model         string         `json:"model,omitempty"`
	System        string         `json:"system,omitempty"`
	Messages      []Message      `json:"messages"`
	Tools         []ToolSpec     `json:"tools,omitempty"`
	Attachments   []Attachment   `json:"attachments,omitempty"`
	JSONMode      *JSONModeSpec  `json:"json_mode,omitempty"`
	Caching       *CachingSpec   `json:"caching,omitempty"`
	Reasoning     *ReasoningSpec `json:"reasoning,omitempty"`
	Params        map[string]any `json:"params,omitempty"`
	RetryOverride *RetryPolicy   `json:"retry_override,omitempty"`
	SessionID     string         `json:"session_id,omitempty"`
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
	return caps
}

// StreamEventKind classifies a streaming chunk.
type StreamEventKind string

const (
	StreamText      StreamEventKind = "text"
	StreamTool      StreamEventKind = "tool"
	StreamReasoning StreamEventKind = "reasoning"
	StreamUsage     StreamEventKind = "usage"
	StreamFinish    StreamEventKind = "finish"
	StreamError     StreamEventKind = "error"
)

// StreamEvent is one delta delivered through a Stream.
type StreamEvent struct {
	Kind      StreamEventKind  `json:"kind"`
	Text      string           `json:"text,omitempty"`
	Tool      *ToolUse         `json:"tool,omitempty"`
	Reasoning *ReasoningBlock  `json:"reasoning,omitempty"`
	Usage     *Usage           `json:"usage,omitempty"`
	Finish    string           `json:"finish,omitempty"`
	Err       string           `json:"err,omitempty"`
	Raw       json.RawMessage  `json:"raw,omitempty"`
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
}

// Cost is the derived currency cost for a Usage snapshot (FR-011).
type Cost struct {
	Currency      string  `json:"currency"`
	Total         float64 `json:"total"`
	InputCost     float64 `json:"input_cost,omitempty"`
	OutputCost    float64 `json:"output_cost,omitempty"`
	CachedCost    float64 `json:"cached_cost,omitempty"`
	ReasoningCost float64 `json:"reasoning_cost,omitempty"`
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
