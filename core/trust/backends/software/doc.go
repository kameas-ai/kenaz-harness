// Package software implements an in-memory test-only [SigningBackend].
//
// # NOT FOR PRODUCTION
//
// This backend holds Ed25519 private keys in process memory. It is gated
// behind the `test_software_backend` build tag — production binaries
// MUST NOT compile this code path. See plan §1.4 (status table) and
// §6.2 (backend → library mapping).
//
// The stub.go variant (no build tag) refuses [Register] so accidental
// enablement is impossible.
//
// Why it exists: it gives the verify pipeline and the [trust.TrustEngine]
// integration tests a deterministic, dependency-free reference signer
// against which the real backends (oskeychain, awskms, yubikey) can be
// tested for envelope-shape parity.
package software
