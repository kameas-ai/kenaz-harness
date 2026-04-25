// Package oskeychain implements the production-default
// [SigningBackend] backed by OS-native secure storage.
//
// # Library mapping (per secrets-keychain D1 / D2)
//
//   - macOS    — Keychain via github.com/zalando/go-keyring
//   - Linux    — Secret Service first, then XDG portal fallback (D2),
//                NEVER a silent file-backed fallback
//   - Windows  — Credential Manager via go-keyring
//
// # Boundary contract (DIRECTIVE_001 / C-001)
//
// Only this subpackage is permitted to import zalando/go-keyring.
// Other core/ packages reach this backend through the [SigningBackend]
// interface.
//
// # Operator notes (R-002 in plan §8 risk register)
//
//   - **macOS first-access prompt.** The first time this backend
//     touches the Keychain, macOS may prompt the operator to allow
//     access. Headless / CI use should pre-grant access via
//     `security` or by importing the entry under an automation
//     account.
//   - **Linux SSH-only boxes.** Secret Service is typically only
//     reachable inside a desktop session. SSH-only environments fall
//     through to the XDG portal; if neither is available, Health()
//     reports HealthUnavailable and the dispatcher fails closed
//     (NFR-007).
//   - **Windows Credential Manager limits.** Entry name + payload
//     character limits are documented per Microsoft; this package
//     surfaces ErrEntryTooLong rather than silently truncating.
//
// # v1.0 status
//
// This file declares the package surface. The full implementation
// lands behind a build tag once the zalando/go-keyring dependency is
// vendored — until then, [New] returns a backend whose Health is
// HealthUnavailable so the dispatcher fails closed.
package oskeychain
