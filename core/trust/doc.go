// Package trust implements the kaneaz-harness cryptographic trust primitive
// for A2A signed agent cards and any future signed surface (context packs,
// bundle manifests, signed policy artifacts).
//
// # Boundary contract (DIRECTIVE_001 / charter C-001)
//
// Trust logic lives only in core/trust/. Backends live exclusively under
// core/trust/backends/<kind>/. No core/ package outside core/trust/backends/
// may import a KMS, HSM, OS-keychain, YubiKey, or PKCS#11 SDK.
//
// External consumers (acp, bundle, context, policy, config) interact with
// core/trust/ through the [TrustEngine] public surface only. Adding a new
// backend or algorithm is a single-package change (NFR-006 / SC-007).
//
// # Stability claim
//
// The signing-backend contract ([SigningBackend]) and the [TrustEngine]
// public API are designed to be stable across v1.x. New algorithms and
// backends slot in behind the same interfaces. Rejection-code enums
// ([RejectionCode]) are append-only — codes are never renamed or removed.
//
// # Append-only audit (charter C-003 / C-005)
//
// Every verification decision and every trust-config change emits exactly
// one append-only audit event into core/event/. No private side-log.
//
// # Fail-closed posture (NFR-007)
//
// Sign operations fail closed when the configured backend reports
// HealthUnavailable; the engine never silently falls back to a different
// backend.
//
// See spec.md and plan.md under
// kitty-specs/a2a-signed-cards-trust-01KQ18P9/.
package trust
