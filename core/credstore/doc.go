// Package credstore is the harness's credential broker layer. It owns
// the raw bytes for every secret the harness manages and is the ONLY
// package outside core/secrets that is permitted to hold a live []byte
// credential value. All other consumers receive an opaque [Handle] or a
// pre-authorised HTTP request via [Store.RoundTrip].
//
// # Design invariants (from spec credential-store-01KQ8TDD)
//
//  1. Raw key material lives in exactly this package and core/secrets.
//     Every other package receives a [Handle] or a just-in-time injected
//     HTTP header — never a []byte credential.
//
//  2. Every credential access is audited. [Store.Use] emits a
//     core/context/audit KindCredentialAccessed event with who, what, when,
//     and which tool-call triggered the access. No raw bytes appear in the
//     audit payload.
//
//  3. Credential issuance is policy-gated (Mission B / cedar-credential-
//     policy-01KQ8TDE adds the Cedar resource type; this mission ships the
//     plumbing with a default-permit posture).
//
//  4. MCP child processes never see credentials in argv. Injection happens
//     at process-spawn via env, sourced through [Store.IssueForMCPSpawn].
//
// # Spec mapping
//
// FR-001 — FR-008, FR-012 of credential-store-01KQ8TDD Mission A.
// WP01 ships the public API surface so WP02-WP09 can fan out in parallel.
//
// See plan.md §1 for the full package layout.
package credstore
