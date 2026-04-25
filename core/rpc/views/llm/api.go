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
	Model string `json:"model"`
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
	ID     string              `json:"id"`
	Name   string              `json:"name"`
	Kind   string              `json:"kind"`
	Model  string              `json:"model"`
	Region string              `json:"region,omitempty"`
	Cred   CredentialReference `json:"cred"`
	// PlaintextAPIKey is consumed by the bindings layer: it is written
	// to the OS keychain under Cred.Locator and then zeroed before any
	// further processing. The personal store never sees this field.
	PlaintextAPIKey string `json:"plaintextApiKey,omitempty"`
}

// TestResult is the structured outcome of TestProvider. The frontend
// renders Success as a pill and Message inline.
type TestResult struct {
	Success   bool   `json:"success"`
	LatencyMS int    `json:"latency_ms"`
	Message   string `json:"message"`
}

// LLMConnectorAPI is the view-scoped accessor for provider metadata and
// streams. Implementations MUST be safe for concurrent use.
type LLMConnectorAPI interface {
	ListProviders(ctx context.Context) ([]Provider, error)
	StartStream(ctx context.Context, providerID string) (subscriptionID string, err error)
	StopStream(ctx context.Context, subscriptionID string) error

	// AddProvider persists a personal provider profile. The supplied
	// PlaintextAPIKey (if any) MUST be written to the OS keychain by
	// the implementation and zeroed before returning. Only the
	// CredentialReference is persisted in providers.json.
	AddProvider(ctx context.Context, in AddProviderInput) error
	// RemoveProvider deletes the personal provider with the given ID.
	// Bundle-derived profiles are not removable through this surface.
	RemoveProvider(ctx context.Context, id string) error
	// TestProvider performs a small probe call (ListModels-equivalent
	// or 1-token completion) to verify the credential resolves and the
	// provider API responds. Errors classified as transient retain
	// Success=false but populate Message.
	TestProvider(ctx context.Context, id string) (TestResult, error)
}
