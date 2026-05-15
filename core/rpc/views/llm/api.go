// Package llm defines the LLMConnectorAPI view-scoped accessor. The
// concrete implementation is wired by the llm-connector mission.
package llm

import (
	"context"
	"time"
)

// Redacted is the display-safe credential view carried over the RPC
// boundary (credential-store-01KQ8TDD WP05). It mirrors
// core/credstore.Redacted but is declared here so this package does
// not take a hard dependency on credstore. Raw credential bytes never
// appear in this struct (DIRECTIVE_001 / FR-006).
type Redacted struct {
	// Display is the human-readable redacted form (e.g. "sk-a…d4f9").
	Display string `json:"display"`
	// Kind is the CredentialReference.Kind string ("keychain", "env", …).
	Kind string `json:"kind"`
	// Locator is the redaction-safe reference ID — NOT the raw locator.
	Locator string `json:"locator"`
}

// Provider is reference-only metadata about a configured LLM provider.
// Credentials live behind secrets-keychain — never returned here.
type Provider struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Tier  string `json:"tier"`
	Kind  string `json:"kind,omitempty"`
	Model string `json:"model"`
	// Models is the authorised set; chat surfaces use it to populate
	// the mid-conversation model switcher. Empty => single-model row
	// — UI falls back to [Model].
	Models []string `json:"models,omitempty"`
	// ModelInfos carries capability metadata (contextWindow, etc.) for
	// each entry in Models. Parallel by position; may be shorter than
	// Models when capability data is unavailable for a given model.
	ModelInfos []ModelInfo `json:"modelInfos,omitempty"`
	// Region is non-empty for kinds that require it (bedrock today).
	Region string `json:"region,omitempty"`
	// Cred surfaces the indirect-reference shape so the UI can render
	// edit forms without storing the original kind elsewhere. The
	// locator is intentionally NOT a credential value — it's the
	// keychain entry name or AWS profile name.
	Cred CredentialReference `json:"cred,omitempty"`
	// Redaction carries the display-safe credential view populated by
	// credstore.Peek (WP05). The wire payload never carries raw key
	// material — consumers read Redaction.Display for UI rendering.
	Redaction Redacted `json:"redaction,omitempty"`
	// Source is "bundle" or "personal" — the UI surfaces this so users
	// know whether a provider came from a signed bundle or their own
	// providers.json store.
	Source string `json:"source,omitempty"`
	// Validated mirrors the most-recent TestProvider outcome for
	// rendering the row's status pill. Reset to false on AddProvider.
	Validated bool `json:"validated,omitempty"`
}

// CredentialReference is the indirect reference shape carried over the
// frontend RPC boundary. Mirrors core/llm.CredentialReference but is
// duplicated here so the rpc surface does not depend on the connector
// package directly. The bindings layer translates between the two.
//
// CredentialReference values that the personal store accepts MUST have
// kind="keychain"; plaintext credentials never appear in this struct.
type CredentialReference struct {
	Kind    string `json:"kind"`
	Locator string `json:"locator"`
}

// AddProviderInput is the payload accepted by AddProvider. Mirrors the
// minimal subset of llm.ProviderProfile needed to drive the personal
// store from the UI.
type AddProviderInput struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Model string `json:"model"`
	// Models is the full set of models the user authorised on this
	// provider. The first entry is the default; the chat-surface
	// model-switcher picks the rest at call time. When empty the
	// backend falls back to [Model] for legacy single-model rows.
	Models []string            `json:"models,omitempty"`
	Region string              `json:"region,omitempty"`
	Cred   CredentialReference `json:"cred"`
	// PlaintextAPIKey is consumed by the bindings layer: it is written
	// to the OS keychain under Cred.Locator and then zeroed before any
	// further processing. The personal store never sees this field.
	PlaintextAPIKey string `json:"plaintextApiKey,omitempty"`
}

// TestResult is the structured outcome of TestProvider. The frontend
// renders Success as a pill and Message inline.
type TestResult struct { // privacy-allow: domain type, not a test fixture
	Success   bool   `json:"success"`
	LatencyMS int    `json:"latency_ms"`
	Message   string `json:"message"`
}

// ModelInfo is the user-pickable model entry returned by ListModels.
// Mirrors core/llm.ModelInfo for the rpc-frontend boundary.
type ModelInfo struct {
	ID            string `json:"id"`
	DisplayName   string `json:"displayName"`
	Description   string `json:"description,omitempty"`
	ContextWindow int    `json:"contextWindow,omitempty"` // max tokens; 0 = unknown
	// MaxOutputTokens is the provider's hard cap on tokens in a single
	// response. 0 / missing means unknown — the UI should not render an
	// explicit cap in that case (backend-context-window-length-01KQ8TD3 WP01).
	MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
}

// AttachmentLimitsView is the wire-safe view of capabilities.AttachmentDescriptor
// returned by GetAttachmentLimits. Zero values mean "unknown/unbounded".
// All byte caps are in bytes; pixel caps are in pixels (W×H).
// (multimodal-io-01KQ8TDF WP04 / FR-007)
type AttachmentLimitsView struct {
	ImageInput              bool     `json:"imageInput"`
	DocumentInput           bool     `json:"documentInput"`
	MaxImageBytes           int64    `json:"maxImageBytes,omitempty"`
	MaxDocumentBytes        int64    `json:"maxDocumentBytes,omitempty"`
	MaxImageCountPerMessage int      `json:"maxImageCountPerMessage,omitempty"`
	MaxImagePixels          int64    `json:"maxImagePixels,omitempty"`
	MaxDocumentPages        int      `json:"maxDocumentPages,omitempty"`
	ImageInputMimeTypes     []string `json:"imageInputMimeTypes,omitempty"`
	DocumentInputMimeTypes  []string `json:"documentInputMimeTypes,omitempty"`
}

// RotationResult is the structured outcome of TestAndRotateKey.
// (provider-keychain-rotation-01KQ8TD9 WP04)
type RotationResult struct {
	Success         bool      `json:"success"`
	Message         string    `json:"message,omitempty"`
	LatencyMS       int       `json:"latency_ms"`
	TestedAt        time.Time `json:"tested_at"`
	// AutoResumeToken is the sub_id of the paused chat turn, populated
	// when the test passed AND a paused turn exists for this profile.
	// Empty when no paused turn exists or when auto-resume is disabled.
	AutoResumeToken string `json:"auto_resume_token,omitempty"`
}

// LLMConnectorAPI is the view-scoped accessor for provider metadata and
// streams. Implementations MUST be safe for concurrent use.
//
// StartStream takes a profile (provider) id and the session id the
// completion is attached to. The chat-ui mission added the second arg
// so per-token chunks can be correlated to the caller-side message
// without smuggling state through the subscription id.
type LLMConnectorAPI interface {
	ListProviders(ctx context.Context) ([]Provider, error)
	// StartStream opens a streaming generation for the given session.
	// modelOverride, when non-empty, must be in the profile's authorised
	// model set; the registry validates and substitutes prof.Model
	// before dispatch. Empty => use the profile default.
	StartStream(ctx context.Context, profileID, sessionID, modelOverride string) (subscriptionID string, err error)
	StopStream(ctx context.Context, subscriptionID string) error

	// AddProvider persists a personal provider profile. The supplied
	// PlaintextAPIKey (if any) MUST be written to the OS keychain by
	// the implementation and zeroed before returning. Only the
	// CredentialReference is persisted in providers.json.
	AddProvider(ctx context.Context, in AddProviderInput) error
	// UpdateProvider replaces an existing personal provider profile.
	// PlaintextAPIKey is OPTIONAL — when empty, the keychain entry is
	// left untouched so users can edit model/region without re-entering
	// the credential. The profile id must already exist; bundle-derived
	// profiles are not editable through this surface.
	UpdateProvider(ctx context.Context, in AddProviderInput) error
	// RemoveProvider deletes the personal provider with the given ID.
	// Bundle-derived profiles are not removable through this surface.
	RemoveProvider(ctx context.Context, id string) error
	// TestProvider performs a small probe call (ListModels-equivalent
	// or 1-token completion) to verify the credential resolves and the
	// provider API responds. Errors classified as transient retain
	// Success=false but populate Message.
	TestProvider(ctx context.Context, id string) (TestResult, error)

	// ListModels probes the provider for the set of models the supplied
	// credential can call. The plaintext key is consumed by the rpc
	// layer and zeroed before this method returns. Returns an empty
	// slice (not an error) when the kind has no ModelLister-capable
	// adapter — the UI then falls back to manual model entry.
	ListModels(ctx context.Context, kind, plaintextApiKey string) ([]ModelInfo, error)

	// ResolveConfirm completes a pending confirm-each tool call. The
	// frontend modal calls this with one of the four canonical
	// decisions ("allow", "deny", "always_allow", "always_deny") to
	// unblock the toolloop goroutine waiting on the request id.
	// Unknown ids return a not-pending error; unknown decisions
	// return a validation error. Safe to call when the confirm-each
	// feature flag is off — the gateway is wired regardless of the
	// flag, only the toolloop chooses whether to invoke it.
	ResolveConfirm(ctx context.Context, requestID, decision string) error

	// UpdateProviderCredential writes a new plaintext API key for
	// profileID directly to the OS keychain and zeroes the in-memory
	// buffer before returning. The "leave blank to keep current"
	// frontend pattern is preserved — the UI ONLY calls this method
	// when the user has entered a new value (credential-store-01KQ8TDD
	// WP05 / FR-007).
	UpdateProviderCredential(ctx context.Context, profileID, plaintext string) error

	// GetAttachmentLimits returns the resolved per-provider attachment
	// capability descriptor for the given provider kind + model. The
	// frontend uses this to replace hard-coded byte caps with
	// descriptor-driven values (multimodal-io-01KQ8TDF WP04 / FR-007).
	// provider is the profile.Kind (e.g. "anthropic", "openai"); model is
	// the model ID string. Returns a zero-value descriptor (everything
	// disabled) when the provider or model is unknown.
	GetAttachmentLimits(ctx context.Context, provider, model string) (AttachmentLimitsView, error)

	// TestAndRotateKey validates plaintextApiKey against the provider's
	// /models endpoint and, on success, overwrites the stored keychain
	// entry and emits a KindProviderKeyRotated audit event.
	//
	// source should be "inline-toast" when invoked from the auth-failure
	// toast or "manual" when invoked from a future explicit rotate button.
	//
	// The plaintext key is consumed and zeroed before this method returns —
	// the caller must not retain a reference to it after the call.
	// (provider-keychain-rotation-01KQ8TD9 WP04)
	TestAndRotateKey(ctx context.Context, profileID, plaintextApiKey, source string) (RotationResult, error)

	// ResumeAfterKeyRotation drives a fresh kernel run for the paused
	// chat turn identified by resumeToken (the sub_id from the RotationResult).
	// It is a no-op when the token does not match an active paused turn.
	// (provider-keychain-rotation-01KQ8TD9 WP04)
	ResumeAfterKeyRotation(ctx context.Context, resumeToken string) error

	// TestProviderKey validates a plaintext API key against the given provider
	// kind and resource host. Unlike TestAndRotateKey, this does NOT write the
	// key to the keychain — it is a read-only probe used by the AddProvider form
	// to display connection status before the user clicks Submit.
	//
	// kind       — the provider kind string ("azure-openai"; others are stubs for now).
	// host       — the provider-specific resource host (Azure: resource hostname,
	//              e.g. "myresource.openai.azure.com"; other kinds: ignored).
	// plaintextKey — the API key to test. Zeroed before this method returns.
	//
	// (azure-openai-adapter-01KQ8VMZ WP03)
	TestProviderKey(ctx context.Context, kind, host, plaintextKey string) (TestProviderKeyResult, error)
}

// TestProviderKeyResult is the structured outcome of TestProviderKey.
// (azure-openai-adapter-01KQ8VMZ WP03)
type TestProviderKeyResult struct {
	OK                 bool   `json:"ok"`
	ModelCount         int    `json:"model_count"`
	DeprecationWarning string `json:"deprecation_warning,omitempty"`
	Message            string `json:"message,omitempty"`
}
