// Package anthropic implements the connector ProviderAdapter contract
// for the Anthropic Messages API (https://docs.anthropic.com/en/api/messages).
//
// DIRECTIVE_001 (mission llm-connector C-001 / C-005): only this
// package is permitted to import or speak Anthropic-specific wire
// types. Every other consumer reaches Anthropic models through the
// connector Registry's typed surface (core/llm.GenerationRequest,
// core/llm.Stream).
//
// The adapter is hand-rolled on stdlib net/http; we deliberately avoid
// a third-party SDK so the harness stays free of opaque dependencies
// and so error classification remains in our control.
//
// Wire-format notes:
//
//   - The adapter always opts into streaming (FR-004 / FR-012). The
//     Messages API streams Server-Sent-Events frames (data: {…}); the
//     parser in stream.go converts those frames into typed
//     llm.StreamEvent values.
//   - Authentication uses the x-api-key header. Credential bytes arrive
//     via the cred parameter of Stream() — the registry resolves the
//     CredentialReference and hands a private byte buffer to the
//     adapter for the lifetime of one request, then zeroizes it.
//   - Errors are normalized to the connector taxonomy (errors.go in
//     core/llm) so the retry middleware and audit pipeline never have
//     to inspect provider-specific shapes.
//
// Capability advertisements live in core/llm/capabilities/data/anthropic.yaml;
// this package exposes Capabilities() that delegates to the embedded
// catalog.
package anthropic
