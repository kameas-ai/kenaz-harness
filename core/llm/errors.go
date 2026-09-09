package llm

import (
	"errors"
	"fmt"
	"strings"
)

// Error sentinels for the typed taxonomy. The retry middleware classifies
// adapter errors via errors.As / errors.Is against the concrete types
// below (ErrTransient, ErrRetryBudgetExhausted, etc.) — these sentinels
// give callers a stable equality target.
var (
	ErrUnknown = errors.New("llm: unknown error")
)

// ErrCapabilityUnsupported is returned when a request opts into a
// capability the (provider, model) does not support (FR-013).
type ErrCapabilityUnsupported struct {
	Provider     string
	Model        string
	Capabilities []Capability
}

func (e *ErrCapabilityUnsupported) Error() string {
	caps := make([]string, len(e.Capabilities))
	for i, c := range e.Capabilities {
		caps[i] = string(c)
	}
	return fmt.Sprintf("llm: capability unsupported by provider %q model %q: %s",
		e.Provider, e.Model, strings.Join(caps, ","))
}

// UnsupportedModalityError is returned by the buildRequest validation
// step when a Message carries an "image" or "document" content block
// targeting a model that does not advertise the corresponding
// capability (multimodal-io FR-010 / A3). The chat surface renders
// Friendly() in place of the raw error text.
type UnsupportedModalityError struct {
	Modality string // "image" | "document"
	Model    string
}

func (e *UnsupportedModalityError) Error() string {
	return fmt.Sprintf("model %q does not support %s blocks", e.Model, e.Modality)
}

func (e *UnsupportedModalityError) Friendly() string {
	return fmt.Sprintf("Model `%s` doesn't support %ss. Switch to a vision-capable model or remove the attachment.", e.Model, e.Modality)
}

// ErrCredentialResolution wraps a failure to resolve a CredentialReference
// at preflight or at request time (FR-003 / FR-019).
type ErrCredentialResolution struct {
	ProfileID string
	Ref       CredentialReference
	Cause     error
}

func (e *ErrCredentialResolution) Error() string {
	// "memory: not found" specifically means the keychain entry has
	// been wiped (e.g. provider added before OS-keychain persistence
	// landed, then Wails restarted). Surface a user-friendly recovery
	// hint instead of the raw backend message.
	if e.Cause != nil &&
		strings.Contains(e.Cause.Error(), "memory: not found") {
		return fmt.Sprintf(
			"llm: API key for provider %q is missing from the keychain. "+
				"Open the providers tab and click Edit on this row to re-paste your key.",
			e.ProfileID,
		)
	}
	return fmt.Sprintf("llm: credential resolution failed for profile %q ref %s: %v",
		e.ProfileID, e.Ref.String(), e.Cause)
}

func (e *ErrCredentialResolution) Unwrap() error { return e.Cause }

// ErrTransient marks a recoverable provider error subject to retry
// (FR-016 / FR-017). Adapters MUST wrap network blips, 408, 425, 429,
// and 5xx responses in ErrTransient.
//
// RetryAfterSec, when > 0, carries the server-mandated backoff from the
// Retry-After or X-RateLimit-Reset-After response headers. The retry
// middleware honors this value (FR-004 / agent-loop-robustness-parity
// WP04): it uses max(computedBackoff, RetryAfterSec * 1000) as the
// actual sleep so rate-limit storms don't retry faster than the server
// requests.
type ErrTransient struct {
	Status        int
	Message       string
	Cause         error
	RetryAfterSec float64 // server-requested backoff in seconds; 0 means not specified
}

func (e *ErrTransient) Error() string {
	if e.Status > 0 {
		return fmt.Sprintf("llm: transient provider error (status=%d): %s", e.Status, e.Message)
	}
	return "llm: transient provider error: " + e.Message
}

func (e *ErrTransient) Unwrap() error { return e.Cause }

// AttemptOutcome records the outcome of one retry attempt for the
// budget-exhausted error.
type AttemptOutcome struct {
	Attempt   int
	Err       error
	BackoffMS int
	ActualMS  int
}

// ErrRetryBudgetExhausted is returned when the retry middleware has
// exhausted MaxAttempts without recovering (FR-016 / US4 Acceptance 3).
type ErrRetryBudgetExhausted struct {
	Attempts []AttemptOutcome
}

func (e *ErrRetryBudgetExhausted) Error() string {
	if last := e.lastErr(); last != nil {
		return fmt.Sprintf("llm: retry budget exhausted after %d attempts; last error: %s",
			len(e.Attempts), last.Error())
	}
	return fmt.Sprintf("llm: retry budget exhausted after %d attempts", len(e.Attempts))
}

// lastErr returns the error from the final recorded attempt, or nil
// when no attempt carried one.
func (e *ErrRetryBudgetExhausted) lastErr() error {
	for i := len(e.Attempts) - 1; i >= 0; i-- {
		if e.Attempts[i].Err != nil {
			return e.Attempts[i].Err
		}
	}
	return nil
}

// Unwrap exposes the last attempt's underlying error so errors.Is /
// errors.As can reach the real provider fault (e.g. *ErrTransient with
// its HTTP status) instead of stopping at the generic budget wrapper.
func (e *ErrRetryBudgetExhausted) Unwrap() error { return e.lastErr() }

// ErrAuth marks an authentication / authorization failure (401/403).
// Non-transient — never retried (FR-017).
type ErrAuth struct {
	Status  int
	Message string
}

func (e *ErrAuth) Error() string {
	return fmt.Sprintf("llm: auth error (status=%d): %s", e.Status, e.Message)
}

// Friendly renders ErrAuth in the model-agnostic, type-specific style of
// the attachment-error family (tool-error-legibility-01PMDL02 WP02): a
// 401/403 is a distinct failure leg from a crashed tool or a rejected
// request, and the recovery action (re-check the credential) differs
// from either.
func (e *ErrAuth) Friendly() string {
	return fmt.Sprintf(
		"Authentication failed (status=%d): %s. Check the provider's API key — open the providers tab, re-paste the key, or verify it hasn't been revoked/rotated.",
		e.Status, e.Message)
}

// ErrInvalidRequest marks a 4xx (other than 408/425/429) provider
// rejection. Non-transient — never retried (FR-017).
type ErrInvalidRequest struct {
	Status  int
	Message string
}

func (e *ErrInvalidRequest) Error() string {
	return fmt.Sprintf("llm: invalid request (status=%d): %s", e.Status, e.Message)
}

// Friendly renders ErrInvalidRequest in the model-agnostic, type-specific
// style of the attachment-error family (tool-error-legibility-01PMDL02
// WP02): a 4xx request rejection is a distinct failure leg from an auth
// failure, a tool crash, or a policy denial — it usually means the
// request shape/parameters themselves were rejected by the provider, not
// that anything about the environment or credentials changed.
func (e *ErrInvalidRequest) Friendly() string {
	return fmt.Sprintf(
		"Request rejected by the provider (status=%d): %s. This usually means malformed parameters or an unsupported option for this model — check the request shape rather than retrying unchanged.",
		e.Status, e.Message)
}

// ErrPaymentRequired marks a 402 provider rejection: the request would
// exceed the account's available credits/balance. Non-transient — never
// retried (FR-017, same rule as ErrAuth/ErrInvalidRequest): retrying
// cannot conjure credits, so classifying this into ErrTransient would
// burn the retry budget on a doomed request.
//
// It is a distinct type from ErrInvalidRequest (rather than folding 402
// into that bucket the way ClassifyStatus's default case does for other
// 4xx codes) because ErrInvalidRequest.Friendly() tells the user to
// "check the request shape" — actively wrong advice for an insufficient-
// credit rejection, which has nothing to do with malformed parameters.
type ErrPaymentRequired struct {
	Status  int
	Message string
}

func (e *ErrPaymentRequired) Error() string {
	return fmt.Sprintf("llm: payment required (status=%d): %s", e.Status, e.Message)
}

// Friendly renders ErrPaymentRequired in the model-agnostic, type-specific
// style of the ErrAuth/ErrInvalidRequest family. It carries no
// provider/profile context on its own — that is added by the
// registry-level decoration, ErrProviderPaymentRequired, mirroring how
// ErrAuth is decorated into ErrProviderAuthFailed. This base Friendly()
// is the fallback for callers that see the raw adapter error before
// decoration (e.g. adapter-level tests, TestKey paths).
func (e *ErrPaymentRequired) Friendly() string {
	msg := e.Message
	if msg == "" {
		msg = "Add credits, or lower max_tokens / shorten the prompt, then try again."
	}
	return fmt.Sprintf("Insufficient credits for this request. %s", msg)
}

// ErrPolicyDenied indicates a policy-engine refusal pre-call.
type ErrPolicyDenied struct {
	Reason string
}

func (e *ErrPolicyDenied) Error() string {
	return "llm: policy denied: " + e.Reason
}

// ErrCancelled marks a caller-initiated cancellation (FR-012).
type ErrCancelled struct {
	Reason string
}

func (e *ErrCancelled) Error() string {
	if e.Reason == "" {
		return "llm: cancelled"
	}
	return "llm: cancelled: " + e.Reason
}

// ErrProviderAuthFailed is the registry-level decoration of *ErrAuth with
// the profile/provider context needed to drive the key-rotation toast
// (provider-keychain-rotation-01KQ8TD9 WP01). It wraps the raw *ErrAuth so
// errors.As works for both types in the same chain:
//
//	var authFailed *ErrProviderAuthFailed
//	var authBare   *ErrAuth
//	errors.As(err, &authFailed) // true — registry context
//	errors.As(err, &authBare)   // true — via Unwrap
type ErrProviderAuthFailed struct {
	Provider  string   // adapter kind: "anthropic" | "openai" | …
	ProfileID string   // the profile that was about to be dispatched
	ModelID   string   // the resolved model (post-override)
	Reason    string   // human-readable copy from the wrapped ErrAuth.Message
	Cause     *ErrAuth // original adapter-level error; reachable via Unwrap
}

func (e *ErrProviderAuthFailed) Error() string {
	return fmt.Sprintf("llm: provider auth failed (provider=%s profile=%s model=%s): %s",
		e.Provider, e.ProfileID, e.ModelID, e.Reason)
}

// Unwrap exposes the underlying *ErrAuth so errors.As(err, &ErrAuth{})
// traverses through ErrProviderAuthFailed.
func (e *ErrProviderAuthFailed) Unwrap() error { return e.Cause }

// ErrProviderPaymentRequired is the registry-level decoration of
// *ErrPaymentRequired with the profile/provider context needed to tell
// the user which of their configured providers is out of credit — the
// same shape as ErrProviderAuthFailed (provider-keychain-rotation-
// 01KQ8TD9 WP01), applied to the 402 leg instead of 401/403. It wraps
// the raw *ErrPaymentRequired so errors.As works for both types in the
// same chain:
//
//	var paymentFailed *ErrProviderPaymentRequired
//	var paymentBare    *ErrPaymentRequired
//	errors.As(err, &paymentFailed) // true — registry context
//	errors.As(err, &paymentBare)   // true — via Unwrap
type ErrProviderPaymentRequired struct {
	Provider  string              // adapter kind: "anthropic" | "openai" | …
	ProfileID string              // the profile that was about to be dispatched
	ModelID   string              // the resolved model (post-override)
	Reason    string              // human-readable copy from the wrapped ErrPaymentRequired.Message
	Cause     *ErrPaymentRequired // original adapter-level error; reachable via Unwrap
}

func (e *ErrProviderPaymentRequired) Error() string {
	return fmt.Sprintf("llm: provider payment required (provider=%s profile=%s model=%s): %s",
		e.Provider, e.ProfileID, e.ModelID, e.Reason)
}

// Unwrap exposes the underlying *ErrPaymentRequired so
// errors.As(err, &ErrPaymentRequired{}) traverses through
// ErrProviderPaymentRequired.
func (e *ErrProviderPaymentRequired) Unwrap() error { return e.Cause }

// Friendly renders ErrProviderPaymentRequired naming the provider and
// profile so a user with several configured providers knows which
// account is out of credit, followed by the provider's own remedy text
// (Reason carries the wrapped ErrPaymentRequired.Message verbatim — the
// 402 body already says "add credits, or lower max_tokens or prompt
// size", so this does not invent a remedy the harness cannot perform;
// it just attributes the provider's own words to the right account).
func (e *ErrProviderPaymentRequired) Friendly() string {
	who := e.Provider
	if e.ProfileID != "" {
		who = fmt.Sprintf("%s (profile %q)", who, e.ProfileID)
	}
	msg := e.Reason
	if msg == "" {
		msg = "This request would exceed your available credits. Add credits, or lower max_tokens / shorten the prompt, then try again."
	}
	return fmt.Sprintf("%s rejected this request for insufficient credits: %s", who, msg)
}

// IsTransient reports whether err should be retried by the middleware.
//
// The classification is by error type (errors.As against ErrTransient)
// not by string match — provider adapters convert their native error
// shapes into ErrTransient before returning, so this function never
// has to inspect provider-specific data.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	var t *ErrTransient
	return errors.As(err, &t)
}

// friendlyErr is implemented by error types in this package (and any
// type wrapping them) that render a model-agnostic, user-actionable
// message via Friendly() in place of the raw error text/chain.
type friendlyErr interface{ Friendly() string }

// FriendlyOr returns the Friendly() text of the first error in err's
// chain that implements Friendly(), provided that text is non-empty.
// Otherwise it returns fallback unchanged.
//
// This is the shared implementation behind two independent call sites
// that both need it and cannot import one another: core/rpc's
// friendlyOr (rpc_error_map.go, the synchronous-RPC boundary) and the
// chat runner's terminal-message assembly (core/rpc/views/agentgraph/
// chat/chat_runner.go, the mid-stream boundary — chat_runner.go's
// "backend-error" default case used to render err.Error() verbatim,
// which by kernel-exit time carries the full graph-node wrapping chain
// ("loop: node ...: body ...: model: node ...: chat: registry stream:
// llm: ..."); that is the bug this function closes for that path).
// core/rpc/views/agentgraph/chat cannot import core/rpc (core/rpc wires
// the chat package, not the reverse), so the logic lives here in
// core/llm instead of being duplicated by hand at each boundary.
func FriendlyOr(err error, fallback string) string {
	var f friendlyErr
	if errors.As(err, &f) {
		if s := f.Friendly(); s != "" {
			return s
		}
	}
	return fallback
}

// ── Attachment pre-flight errors (multimodal-io-01KQ8TDF FR-002) ─────────
//
// These errors are returned by capabilities.Gate.CheckAttachments before any
// wire call. Each carries enough context for the frontend's errors.ts
// friendly() to render a provider-specific, user-actionable message.

// ErrAttachmentTooLarge is returned when an attachment exceeds the
// per-provider byte cap (MaxImageBytes / MaxDocumentBytes).
type ErrAttachmentTooLarge struct {
	Provider string
	Mime     string
	Given    int64 // actual byte size
	Cap      int64 // provider limit
}

func (e *ErrAttachmentTooLarge) Error() string {
	return fmt.Sprintf("llm: attachment too large for %s (mime=%s given=%d cap=%d)",
		e.Provider, e.Mime, e.Given, e.Cap)
}

func (e *ErrAttachmentTooLarge) Friendly() string {
	givenMiB := float64(e.Given) / (1024 * 1024)
	capMiB := float64(e.Cap) / (1024 * 1024)
	return fmt.Sprintf("Attachment is too large (%.1f MiB). Provider %q accepts at most %.0f MiB per attachment.",
		givenMiB, e.Provider, capMiB)
}

// ErrAttachmentMimeUnsupported is returned when the attachment MIME type
// is not in the provider's allowed list. For OpenAI Chat Completions this
// is how PDF inputs are rejected (document_input: false in the YAML).
type ErrAttachmentMimeUnsupported struct {
	Provider string
	Mime     string
}

func (e *ErrAttachmentMimeUnsupported) Error() string {
	return fmt.Sprintf("llm: attachment MIME type %q not supported by %s", e.Mime, e.Provider)
}

func (e *ErrAttachmentMimeUnsupported) Friendly() string {
	if e.Mime == "application/pdf" {
		return fmt.Sprintf(
			"Provider %q does not accept PDF attachments via this API. "+
				"Switch to Anthropic, Bedrock-Claude, or convert the PDF pages to images.",
			e.Provider)
	}
	return fmt.Sprintf("Provider %q does not support attachment type %q.", e.Provider, e.Mime)
}

// ErrAttachmentCountExceeded is returned when the number of image blocks
// in a single message exceeds MaxImageCountPerMessage.
type ErrAttachmentCountExceeded struct {
	Provider string
	Given    int
	Cap      int
}

func (e *ErrAttachmentCountExceeded) Error() string {
	return fmt.Sprintf("llm: too many image attachments for %s (given=%d cap=%d)",
		e.Provider, e.Given, e.Cap)
}

func (e *ErrAttachmentCountExceeded) Friendly() string {
	return fmt.Sprintf("Too many images in one message (you have %d; provider %q allows at most %d).",
		e.Given, e.Provider, e.Cap)
}

// ErrAttachmentDimensionExceeded is returned when an image's pixel count
// exceeds MaxImagePixels for the provider.
type ErrAttachmentDimensionExceeded struct {
	Provider string
	Given    int64 // actual pixel count (W*H)
	Cap      int64 // provider limit
}

func (e *ErrAttachmentDimensionExceeded) Error() string {
	return fmt.Sprintf("llm: image pixels exceed limit for %s (given=%d cap=%d)",
		e.Provider, e.Given, e.Cap)
}

func (e *ErrAttachmentDimensionExceeded) Friendly() string {
	return fmt.Sprintf("Image is too large (%.1f MP). Provider %q accepts at most %.1f MP. Resize the image before attaching.",
		float64(e.Given)/1_000_000, e.Provider, float64(e.Cap)/1_000_000)
}

// ErrUnsupportedFormat is returned when the caller requests a ResponseFormat
// mode that the (provider, model) does not support AND no fallback is possible.
// Specifically: Mode="grammar" when CapGrammar is false — grammar cannot be
// emulated via prompt engineering (structured-output-and-grammar-01KX5R8A FR-005).
type ErrUnsupportedFormat struct {
	Provider string
	Model    string
	Mode     string
}

func (e *ErrUnsupportedFormat) Error() string {
	return fmt.Sprintf("llm: response format %q not supported by provider %q model %q",
		e.Mode, e.Provider, e.Model)
}

// IsUnsupportedFormat reports whether err is or wraps ErrUnsupportedFormat.
func IsUnsupportedFormat(err error) bool {
	if err == nil {
		return false
	}
	var t *ErrUnsupportedFormat
	return errors.As(err, &t)
}

// ErrResponseValidationFailed is returned when the model's output failed
// schema validation after all retries (structured-output-and-grammar-01KX5R8A FR-006).
// The caller may inspect SchemaError for the validation message and Raw for
// the first 500 characters of the invalid response for debugging.
type ErrResponseValidationFailed struct {
	Mode        string // "json" | "json_schema" | "grammar"
	SchemaError string // human-readable validation error
	Raw         string // first 500 chars of the invalid response (for debugging)
}

func (e *ErrResponseValidationFailed) Error() string {
	if e.SchemaError != "" {
		return fmt.Sprintf("llm: response validation failed (mode=%s): %s", e.Mode, e.SchemaError)
	}
	return fmt.Sprintf("llm: response validation failed (mode=%s)", e.Mode)
}

// ErrAttachmentEncrypted is returned when a PDF is password-protected and
// cannot be parsed for page-count validation.
type ErrAttachmentEncrypted struct{}

func (e *ErrAttachmentEncrypted) Error() string {
	return "llm: PDF attachment is password-protected"
}

func (e *ErrAttachmentEncrypted) Friendly() string {
	return "PDF is password-protected. Remove the password and re-attach."
}

// ErrAttachmentAudioUnsupported is returned unconditionally for any audio
// MIME type (audio/*) on all providers. Audio input is deferred to a future
// mission (multimodal-io-01KQ8TDF locked decision Q19.2).
type ErrAttachmentAudioUnsupported struct {
	Mime string
}

func (e *ErrAttachmentAudioUnsupported) Error() string {
	return fmt.Sprintf("llm: audio attachment type %q is not supported", e.Mime)
}

func (e *ErrAttachmentAudioUnsupported) Friendly() string {
	return "Audio input is not yet supported. Audio is planned for a future mission."
}

// ── Multimodal output errors (multimodal-io-extended-01KQ8TD2 WP01) ──────────

// ErrJSONModeInvalid is returned when a JSONModeSpec is structurally invalid
// (e.g. Schema present but Enabled=false, or Schema is not valid JSON).
type ErrJSONModeInvalid struct {
	Reason string
}

func (e *ErrJSONModeInvalid) Error() string {
	return "llm: invalid json_mode spec: " + e.Reason
}

// ErrUnsupportedJSONShape is returned when a caller supplies a JSON schema
// that the target provider/model cannot represent (e.g. Ollama with a
// JSON schema on a grammar-only model, or a schema with unsupported keywords).
type ErrUnsupportedJSONShape struct {
	Provider string
	Model    string
	Reason   string
}

func (e *ErrUnsupportedJSONShape) Error() string {
	return fmt.Sprintf("llm: json schema not supported by %s/%s: %s",
		e.Provider, e.Model, e.Reason)
}

// ErrGeneratedImageTooLarge is returned when a model-generated image's byte
// size exceeds the MaxGeneratedImageBytes cap configured in settings. The
// image is NOT captured; the LLM node emits a warn log and drops the image.
type ErrGeneratedImageTooLarge struct {
	Given int64 // actual byte size
	Cap   int64 // configured cap
}

func (e *ErrGeneratedImageTooLarge) Error() string {
	return fmt.Sprintf("llm: generated image too large (given=%d cap=%d)", e.Given, e.Cap)
}

// ErrFeatureDisabled is returned when a request opts into a feature that
// has been disabled via a HARNESS_* environment flag (e.g.
// HARNESS_MULTIMODAL_OUT=off). Non-transient — never retried.
type ErrFeatureDisabled struct {
	Feature string // e.g. "multimodal_out"
	EnvVar  string // e.g. "HARNESS_MULTIMODAL_OUT"
}

func (e *ErrFeatureDisabled) Error() string {
	return fmt.Sprintf("llm: feature %q disabled (set %s=on to enable)", e.Feature, e.EnvVar)
}

// ErrUnsupportedFeature is returned when a request opts into a feature
// or knob that the (provider, model) does not support AND no fallback
// is applicable (FR-010 of provider-implementation-uniformity-01KQ8V4F).
//
// The Hint field carries a user-visible explanation of what the caller
// can do instead (e.g. "switch to a model that supports seed" or
// "use AnthropicThinkingBudget instead of OpenAIEffort on Claude").
//
// The frontend surfaces this via the ComposerError toast (WP08).
type ErrUnsupportedFeature struct {
	ModelID string // e.g. "gpt-3.5-turbo"
	Feature string // e.g. "seed", "json_schema", "reasoning.openai_effort"
	Hint    string // optional user-visible recovery hint
}

func (e *ErrUnsupportedFeature) Error() string {
	if e.Hint != "" {
		return fmt.Sprintf("llm: feature %q not supported by model %q: %s", e.Feature, e.ModelID, e.Hint)
	}
	return fmt.Sprintf("llm: feature %q not supported by model %q", e.Feature, e.ModelID)
}

// IsUnsupportedFeature reports whether err is or wraps ErrUnsupportedFeature.
func IsUnsupportedFeature(err error) bool {
	if err == nil {
		return false
	}
	var t *ErrUnsupportedFeature
	return errors.As(err, &t)
}

// ErrCustomEndpointMissingCapability is returned when a request targets
// a custom OpenAI-compatible endpoint and the probed capability matrix
// indicates the required capability is not supported. This error is
// returned before any wire call.
//
// (custom-openai-compatible-endpoint-01KQ8VN0 WP05)
type ErrCustomEndpointMissingCapability struct {
	// Endpoint is the base URL of the custom endpoint.
	Endpoint string
	// Capability is the missing capability name (e.g. "tool_calling").
	Capability string
	// ProfileID is the ProviderProfile.ID of the failing profile.
	ProfileID string
}

func (e *ErrCustomEndpointMissingCapability) Error() string {
	return fmt.Sprintf("llm: custom endpoint %q does not support %q (probed capability matrix)",
		e.Endpoint, e.Capability)
}
