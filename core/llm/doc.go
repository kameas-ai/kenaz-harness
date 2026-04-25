// Package llm is the provider-agnostic surface for model invocation.
//
// Architectural invariant (Charter DIRECTIVE_001 / spec C-001 / FR-018):
// no package outside core/llm/<provider>/ may import a provider SDK. The
// public types, the Registry façade, and the ProviderAdapter contract
// defined here are the single seam through which the harness reaches
// every supported model provider.
//
// Sub-packages:
//
//   - registry/        in-memory adapter + profile registry (WP02)
//   - capabilities/    capability descriptors and per-call gate (WP03)
//   - credref/         bridge from CredentialReference to core/secrets (WP03)
//   - retry/           exponential-backoff middleware with jitter (WP05)
//   - events/          audit emitter that writes to the harness event log (WP04)
//   - cost/            token-usage to cost reducer with operator override (WP11)
//   - anthropic/, openai/, openrouter/, bedrock/, ollama/  provider adapters
package llm
