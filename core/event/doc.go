// Package event is the harness's append-only audit and replay substrate.
//
// DIRECTIVE_001 / C-001: all event-log logic lives under core/event/. No
// other package writes to the underlying tables directly. Consumers reach
// the log through the public API exposed here:
//
//   - Emitter   — the only ingress for writes (Append). Non-bypassable
//     redaction runs unconditionally inside Append (C-004).
//   - Reader    — indexed query API by session, kind, time, emitter, and
//     content (FTS5).
//   - Verifier  — chain walk; tamper / truncation report.
//   - Replayer  — deterministic, byte-stable session iterator.
//   - Brancher  — child session whose initial state matches the parent
//     at a chosen event id.
//   - Retention — keep_all default; archive / truncate are explicit and
//     themselves recorded as events.
//
// The package exposes no Update / Delete / Patch surface (FR-003 / C-002):
// once written, an event is immutable. Corrections are new "supersession"
// events that reference the original by id.
//
// Identity primitives:
//
//   - EventID is a process-monotonic ULID (lexicographically sortable).
//   - EmitterID is a namespaced source identifier validated against an
//     allowlist documented in plan §5.6.
//   - Kind is a stable, namespaced event-kind id; unknown kinds are
//     preserved verbatim so older readers stay forward-compatible.
//
// Cryptographic spine:
//
//   - Payload bytes are canonicalized (stable JSON) before hashing.
//   - PayloadHash uses BLAKE3 (deviation note: see core/event/chain for
//     hash-function pinning and the ADR commitment in plan §11).
//   - Each session is its own hash chain; the global ULID PK provides a
//     sortable cross-session ordering index (resolves spec OQ-1 default).
package event
