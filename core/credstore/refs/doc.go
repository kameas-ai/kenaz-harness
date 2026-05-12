// Package refs defines the model-side secret reference grammar and the
// boundary-time resolver that substitutes references in tool arguments.
//
// # Reference grammar
//
// A secret reference is an opaque token the model places in any string
// the harness later interprets on its behalf:
//
//	@secret:<locator>
//	@secret:<locator>#<field>
//
// where <locator> is the keychain locator (e.g. "user:example-api-token",
// "provider:bedrock-prod") and the optional #<field> picks a named field
// from a multi-part credential (e.g. "#aws_session_token").
//
// References are case-sensitive. A typo is an error, not a silent miss.
//
// # Resolver
//
// [Resolver] is the single entry point for reference substitution. It
// is constructed once per session and wires together the credstore
// [credstore.Store], the Cedar policy gate, the per-turn [Sanitizer],
// and the audit [audit.Emitter].
//
// The fundamental contract (FR-002, NFR-001):
//   - Resolution happens at the LAST moment before bytes leave the process.
//   - Resolved bytes are zeroed within one operation of the boundary call
//     returning (FR-003).
//   - Resolved plaintext never reaches the model, the audit log payload,
//     the session history, or the streaming buffer.
//
// # Sanitizer
//
// [Sanitizer] maintains a per-turn fingerprint set (SHA-256 of each
// plaintext resolved during the turn). Every tool result content block and
// every streamed assistant token is scanned against this set; matches are
// replaced with "[redacted: <locator>]". The fingerprint set is cleared at
// end-of-turn (NFR-007).
//
// # FR mapping
//
//   - FR-001: reference grammar — [Parse], [IsReferenceToken], [FindAll]
//   - FR-002: boundary-time resolution — [Resolver.Substitute]
//   - FR-003: zero-on-return — [ZeroBuffer]
//   - FR-005: list_secrets discovery — external; this package is the resolver only
//   - FR-006: Cedar gate — [Resolver.Resolve] delegates to the wired Cedar hook
//   - FR-007: sanitizer — [Sanitizer]
//   - FR-008: per-session budget — [Budget]
//   - NFR-004: containment — this package is the only place outside core/credstore
//     and core/secrets that holds []byte credential material
package refs
