// Package llm defines the LLMConnectorAPI view-scoped accessor. The
// concrete implementation is wired by the llm-connector mission.
package llm

import "context"

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
	// Region is non-empty for kinds that require it (bedrock today).
	Region string `json:"region,omitempty"`
	// Cred surfaces the indirect-reference shape so the UI can render
	// edit forms without storing the original kind elsewhere. The
	// locator is intentionally NOT a credential value — it's the
	// keychain entry name or AWS profile name.
	Cred CredentialReference `json:"cred,omitempty"`
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
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Description string `json:"description,omitempty"`
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
}
