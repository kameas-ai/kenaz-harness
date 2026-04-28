# Data Model (Discovery Draft) — Secrets and OS Keychain Abstraction

## Entities

### Entity: CredentialReference
- **Description**: declarative pointer to where a credential lives. Stored in configuration in place of the value.
- **Attributes**:
  - `kind` (enum: `env`, `keychain`, `file`, `aws_profile`, `aws_kms`, `yubikey_piv`, `pkcs11`)
  - `locator` (kind-specific) — env var name, keychain entry name, file path, AWS profile, KMS ARN, PIV slot id, PKCS#11 token URI.
  - `consumer_id` (string, optional) — declares which consumer is intended to use this reference.
- **Identifiers**: `(kind, locator)` tuple.
- **Lifecycle**: declared in bundle config / charter / RPC. Validated at pre-flight. Resolved at use time.

### Entity: Backend
- **Description**: pluggable resolver for one or more reference kinds. Lives in its own package.
- **Attributes**:
  - `kind` (string)
  - `health` (enum: `ok`, `degraded`, `unavailable`)
  - `supported_ref_kinds` (list)
  - `platform_constraints` (list, optional)
- **Implementations (v1)**:
  - **OS Keychain (default)**: wraps `zalando/go-keyring`.
  - **Linux Sandboxed**: wraps `org.freedesktop.portal.Secret` for Flatpak/Snap.
  - **Environment**: `os.Getenv` with optional default-empty-error.
  - **File**: file path; for headless/CI with optional argon2id-derived KEK envelope.
  - **AWS KMS**: envelope encryption via AWS Encryption SDK for Go.
  - **YubiKey PIV**: `go-piv/piv-go` v2.6.0.
  - **PKCS#11** (deferred): `miekg/pkcs11` + `ThalesGroup/crypto11`.
- **Lifecycle**: registered at startup. Health probed during pre-flight.

### Entity: Secret (in-memory, short-lived)
- **Description**: runtime representation of a resolved credential. Backed by `[]byte`. Never `string`.
- **Attributes**:
  - `bytes` (`[]byte`) — visible only through methods that take a closure receiving the slice.
  - `acquired_at` (timestamp)
  - `consumer_id` (string)
  - `reference_id` (string)
- **Methods**: `Use(func([]byte) error)`, `Destroy()`.
- **Lifecycle**: created by `Backend.Resolve`. Bounded by consumer call. memguard variant is opt-in hardening.

### Entity: ResolutionEvent
- **Description**: append-only event log entry recording a resolution attempt.
- **Attributes**:
  - `event_id` (ULID)
  - `consumer_id` (string)
  - `reference_kind` (enum) — never the locator value.
  - `backend_kind` (enum)
  - `outcome` (enum: `ok`, `not_found`, `permission_denied`, `backend_unavailable`, `format_invalid`, `rotated_mid_use`)
  - `latency_ms` (int)
  - `cache_hit` (bool)
  - `emitted_at` (timestamp)
- **Lifecycle**: append-only. Never carries the resolved value.

### Entity: PreFlightResult
- **Description**: per-reference status produced at startup; drives whether the harness starts.
- **Attributes**:
  - `reference_id` (string)
  - `status` (enum: `resolvable`, `unresolvable`, `optional_unresolvable`, `backend_degraded`)
  - `error_code` (enum)
  - `tested_at` (timestamp)

## Relationships

| Source | Relation | Target | Cardinality | Notes |
|---|---|---|---|---|
| Bundle / Charter / RPC payload | declares | CredentialReference | 1:N | One config carries many references. |
| CredentialReference | dispatched to | Backend | N:1 | Routed by `kind`. |
| Backend | produces | Secret | 1:N | Each successful resolve produces a Secret. |
| Secret | consumed by | Consumer call (LLM, signing, A2A peer auth, DB encryption) | 1:1 | Bounded by call. |
| Resolution attempt | emits | ResolutionEvent | 1:1 | Every attempt logged; redacted. |
| Pre-flight pass | emits | PreFlightResult | 1:N | Snapshot at startup. |
| OS keychain entry (external) | stores material for | CredentialReference (`kind=keychain`) | 1:1 | Locator is entry name. |
| AWS KMS data key (external) | wraps | local DEK consumed by callers | 1:N | Envelope-encryption pattern. |

## Validation & Governance

- **Data quality**:
  - References MUST validate against the backend's locator schema at pre-flight.
  - Secrets MUST be `[]byte`. Lint enforces it.
  - Every Secret MUST have a deterministic destroy path (`Destroy()` or `Use(...)`).
  - Backends MUST refuse silent fallback to a less-secure backend.
  - Linux deployments MUST detect sandbox status and route through the XDG portal Secret backend automatically when applicable.
- **Compliance**:
  - Resolved values MUST NOT enter event log entries, RPC payloads beyond the immediate consumer, configuration files, lockfiles, error messages, or panic traces.
  - macOS Keychain access prompts surfaced to the user as deliberate UX.
  - Pre-flight failures emit ResolutionEvents; the operator's startup output names every missing reference.
- **Source of truth**:
  - The OS keychain (or KMS / PIV / etc.) is authoritative for the credential value.
  - Bundle / charter / RPC config is authoritative for which references are needed.
  - The event log is authoritative for resolution history.
