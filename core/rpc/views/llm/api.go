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
	StartStream(ctx context.Context, profileID, sessionID string) (subscriptionID string, err error)
	StopStream(ctx context.Context, subscriptionID string) error
}
