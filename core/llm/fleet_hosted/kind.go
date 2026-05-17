// Package fleet_hosted implements the fleet-hosted inference LLM adapter.
//
// The adapter speaks the OpenAI Chat Completions wire format against the
// fleet-hosted inference endpoint (<EnvProfile.FleetBaseURL>/llm/v1/chat/completions).
// Authentication is provided by the fleet Bearer token fetched from the OS
// keychain via the fleet client's credential store — the frontend profile
// carries a zero-length credential; the real token is injected here.
//
// The adapter is only registered in the LLM registry when the capability
// CapHostedInference is enabled (checked via a callback at resolve time).
// This means a tier change propagates within one poller interval (~5 min)
// without requiring a restart.
//
// (fleet-capability-surface-01NDFSEX09 WP13)
package fleet_hosted

// Kind is the canonical provider kind for fleet-hosted inference.
// It matches the ProviderProfile.Kind value written by the frontend.
const Kind = "fleet-hosted"
