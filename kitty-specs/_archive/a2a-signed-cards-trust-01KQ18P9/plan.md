# Implementation Plan — A2A Signed Agent Cards and Cross-Instance Trust

**Mission**: `a2a-signed-cards-trust-01KQ18P9`
**Mission ID**: `01KQ18P9GXM2P3HM248XV4TB8E`
**Feature Branch**: `feat/a2a-signed-cards-trust-01KQ18P9`
**Target / Merge Branch**: `main`
**Created**: 2026-04-25
**Status**: Draft (Phase 1 design — pre-tasks)

> **Branch contract**: Plan is authored on the project root checkout currently
> at `main`. All implementation lands on `feat/a2a-signed-cards-trust-01KQ18P9`
> and is squash-merged to `main` via PR per charter Branch Strategy. No
> worktrees are created by `/spec-kitty.plan`.

---

## 1. Overview

### 1.1 What this mission ships

The cryptographic trust *primitive* that lets a kaneaz-harness instance prove
that an inbound A2A AgentCard (and any future signed surface — context packs,
bundles, policy artifacts) really originated from the org / team / individual
it claims. Concretely:

- A `core/trust/` package that owns trust-anchor configuration, signature
  verification, key rotation with overlap, revocation enforcement, audit
  emission, and a stable signing-backend abstraction.
- Three signing backends at v1.0: `software` (test only, in-memory), `oskeychain`
  (default per FR-009), `awskms` (opt-in, ships in OSS behind a build tag per
  C-006). Two more under the same contract for v1.x / v2: `yubikey`
  (`go-piv/piv-go`) and `pkcs11` (deferred — interface-only stub).
- A2A v1.0 *Signed Agent Cards* envelope conformance (C-004), implemented by
  reusing `github.com/a2aproject/a2a-go` types where they exist and
  hand-rolling only what the SDK does not expose.
- Append-only audit emission into the shared event log (`core/event/`) for every
  verification decision, anchor change, rotation, revocation ingestion, and
  backend health transition.

### 1.2 Scope boundary (PKI primitive only)

This mission is the **trust primitive**. It does **not** define:

- The *content* of shared context (org/team/personal packs) — that is
  `shared-context-distribution-01KQ18PA`, which calls into this package.
- Bundle integrity verification — that is `bundle-format-resolver-01KQ1A3J`,
  which calls into this package.
- A signed-policy artifact format — that is `policy-engine-01KQ1A3N`, which
  calls into this package.
- Operator identity / account model — explicitly deferred (charter, secrets-
  keychain spec).

> Anything that needs cryptographic provenance over an A2A-shaped payload
> consumes the verification API exposed here, in line with FR-012.

### 1.3 Charter alignment

| Constraint | How this plan honors it |
|---|---|
| `DIRECTIVE_001` (boundaries) | Trust logic lives only in `core/trust/`. Backends in `core/trust/backends/<kind>/`. No KMS / OS-keychain / HSM SDK is imported by any other `core/` package. |
| `DIRECTIVE_003` (ADR for material decisions) | Three planning-phase ADRs queued: `adr-trust-001-default-algorithm`, `adr-trust-002-oss-vs-enterprise-backend-split`, `adr-trust-003-revocation-distribution-v1`. |
| Charter local-first | Default backend is OS keychain. KMS / HSM are opt-in. Verification is an in-process operation; no network call is made on the verification hot path beyond optional revocation freshness checks (configurable, off by default; manual pull per FR-007 default). |
| Charter append-only event log | All trust events flow through `core/event/`. No private side-log. |
| C-002 / NFR-004 (no plaintext keys) | Private keys are never inline in config, lockfile, RPC, event log, or temp files; only backend references (keychain entry name, KMS key ARN, PIV slot) cross trust boundaries. |
| C-004 (A2A v1 envelope) | Signed AgentCards conform to A2A v1.0 Signed Agent Cards. We do not invent a parallel envelope. |
| C-005 / NFR-005 (SOC 2) | Every verification, every anchor change, every rotation, every revocation ingestion produces exactly one event log entry with sufficient detail to replay the decision. |
| C-006 (OSS / enterprise split) | KMS / HSM backends ship in OSS behind build tags; commercial enterprise builds enable them. The signing-backend contract is identical across editions. |

### 1.4 What's *implemented* vs *interface-only* at v1.0

| Component | v1.0 status |
|---|---|
| `core/trust/` engine | implemented |
| `oskeychain` backend (macOS / Linux Secret Service / Windows Cred Manager) | implemented |
| `software` backend (test only) | implemented (test-only build tag) |
| `awskms` backend | implemented behind `kms_aws` build tag |
| `yubikey` backend | interface-conformant stub at v1.0; full impl in v1.x |
| `pkcs11` backend | interface only at v1.0; impl in v2 |
| Ed25519 algorithm | implemented (default) |
| ECDSA-P256, RSA-PSS algorithms | interface-conformant; opt-in operator config in v1.x |
| Manual revocation pull | implemented (FR-007 default) |
| Automatic revocation distribution | deferred to v1.x |

---

## 2. Architectural Placement

### 2.1 Package layout

```
core/
├── trust/                         (NEW — this mission)
│   ├── trust.go                   public engine (TrustEngine, factory)
│   ├── verify.go                  verification pipeline
│   ├── sign.go                    signing dispatcher (delegates to backend)
│   ├── anchor.go                  anchor store + precedence resolver
│   ├── rotation.go                key-rotation / overlap-window logic
│   ├── revocation.go              revocation cache + ingestion
│   ├── policy.go                  algorithm policy + clock-skew tolerance
│   ├── envelope.go                A2A v1 Signed Agent Cards envelope adapter
│   ├── audit.go                   trust → event-log mapper
│   ├── errors.go                  rejection-reason taxonomy (FR-017)
│   ├── config.go                  load / pre-flight (FR-014)
│   ├── doc.go                     package docs
│   ├── backends/
│   │   ├── backend.go             SigningBackend interface
│   │   ├── software/              in-memory (test-only build tag)
│   │   ├── oskeychain/            zalando/go-keyring wrapper (default)
│   │   ├── awskms/                aws-sdk-go-v2/service/kms wrapper
│   │   ├── yubikey/               go-piv/piv-go wrapper
│   │   └── pkcs11/                miekg/pkcs11 wrapper (v2)
│   └── internal/
│       ├── algo/                  algorithm registry (Ed25519, ECDSA, RSA-PSS)
│       └── fingerprint/           public-key fingerprinting
```

**Why this shape**: every backend imports its own SDK; `core/trust/` itself
imports none of those SDKs; everything outside `core/trust/` calls only the
public `TrustEngine` API. Adding a new algorithm or a new backend is a
*single-package* change, satisfying NFR-006 and SC-007.

### 2.2 Consumer relationship

| Consumer | Why it calls `core/trust/` |
|---|---|
| `core/acp/` (acp-orchestration) | Verifies inbound A2A AgentCards before any Skill is invoked. Signs outbound AgentCards on publish. |
| `core/bundle/` (bundle-format-resolver) | Verifies signed bundle manifests. |
| `core/context/` (shared-context-distribution) | Verifies signed context-pack provenance. |
| `core/policy/` (policy-engine) | Verifies signed policy artifacts. |
| `core/config/` | Pre-flight validates trust-anchor config at startup (FR-014). |
| `core/event/` | Receives audit emissions. (Reverse direction — `core/event/` does not call into `core/trust/`.) |

`core/trust/` itself imports `core/event/` (audit emit), `core/secrets/` (for
backend dispatch — see §6.2), and `core/storage/` (anchor + revocation persistence
through storage-foundations). It imports nothing from `frontend/`, `rpc/`, or
Wails (DIRECTIVE_001).

---

## 3. Public API (Illustrative Go signatures)

These are *contracts*, not final implementations. Names track the spec's Key
Entities.

```go
package trust

// TrustEngine is the only entry point external packages use.
type TrustEngine interface {
    // Verify a payload + signature envelope against configured anchors.
    // Always returns exactly one VerificationResult; never returns
    // (nil, nil). Always emits exactly one audit event.
    Verify(ctx context.Context, payload []byte, env Envelope, opts VerifyOptions) (VerificationResult, error)

    // Sign a payload using the named local identity. Backend dispatch
    // determined by IdentityRef. Fails closed on backend unavailable
    // (NFR-007).
    Sign(ctx context.Context, payload []byte, ident IdentityRef, opts SignOptions) (Envelope, error)

    // Anchor management. Each call emits an audit event.
    InstallAnchor(ctx context.Context, a Anchor) error
    RemoveAnchor(ctx context.Context, anchorID string) error
    ListAnchors(ctx context.Context) ([]Anchor, error)

    // Rotation with configurable overlap window (FR-005, FR-013).
    BeginRotation(ctx context.Context, anchorID string, newKey PublicKey, overlap time.Duration) error
    CompleteRotation(ctx context.Context, anchorID string) error

    // Revocation ingestion (FR-006, FR-007).
    IngestRevocation(ctx context.Context, rec RevocationRecord) error

    // Pre-flight validation invoked by core/config at startup (FR-014).
    Preflight(ctx context.Context) ([]PreflightFinding, error)
}

// SigningBackend is implemented by each backend subpackage. The TrustEngine
// dispatches by IdentityRef.Backend.
type SigningBackend interface {
    Kind() BackendKind                       // "software" | "oskeychain" | "awskms" | ...
    Health(ctx context.Context) HealthStatus // ok | degraded | unavailable
    SupportedAlgorithms() []Algorithm
    Sign(ctx context.Context, ref BackendRef, alg Algorithm, payload []byte) ([]byte, error)
    PublicKey(ctx context.Context, ref BackendRef) (PublicKey, error)
}

// Anchor is the unit of operator-configured trust (FR-003).
type Anchor struct {
    AnchorID    string         // stable id, operator-assigned or derived from key
    Kind        AnchorKind     // raw_public_key | org_identifier | pinned_peer
    PublicKey   PublicKey      // present for raw_public_key + pinned_peer
    OrgID       string         // present for org_identifier
    Algorithm   Algorithm
    InstalledAt time.Time
    Metadata    map[string]string
    // Rotation state, if mid-rotation:
    PreviousKey *PublicKey
    OverlapEnds *time.Time
}

// VerificationResult is the typed outcome (FR-017).
type VerificationResult struct {
    Decision      Decision        // accepted | rejected
    AnchorID      string          // present iff accepted
    Algorithm     Algorithm
    CacheState    CacheState      // fresh | grace | stale (rotation overlap signal)
    RejectionCode RejectionCode   // present iff rejected
    Detail        string
    EvaluatedAt   time.Time
}

// RejectionCode = the stable taxonomy from FR-017.
type RejectionCode string
const (
    RejSignatureInvalid    RejectionCode = "signature_invalid"
    RejAlgorithmNotPermit  RejectionCode = "algorithm_not_permitted"
    RejAnchorMissing       RejectionCode = "anchor_missing"
    RejAnchorRemoved       RejectionCode = "anchor_removed"
    RejKeyRevoked          RejectionCode = "key_revoked"
    RejKeyExpired          RejectionCode = "key_expired"
    RejIdentityCollision   RejectionCode = "identity_collision"
    RejClockSkewExceeded   RejectionCode = "clock_skew_exceeded"
    RejChainDepthExceeded  RejectionCode = "chain_depth_exceeded"
)

// RevocationRecord — signed assertion that a key id or identity is no
// longer trusted as of EffectiveAt (FR-006).
type RevocationRecord struct {
    RevocationID string
    SubjectKind  RevocationSubject // key_id | identity
    SubjectID    string
    EffectiveAt  time.Time
    Reason       string
    IssuedBy     string            // anchor id authorized to revoke
    Signature    []byte            // signature over the record by IssuedBy
}
```

> The full set lives in `core/trust/types.go` and will be expanded during
> implementation. These are the surfaces the spec's FRs and the consumer
> packages need to plan against.

---

## 4. Internal Layering

```
                  ┌────────────────────────────────────────┐
                  │ Public TrustEngine (Verify/Sign/...)   │
                  └─────────────┬──────────────────────────┘
                                │
        ┌───────────────────────┼───────────────────────────┐
        ▼                       ▼                           ▼
┌──────────────┐       ┌─────────────────┐         ┌────────────────┐
│ Anchor Store │       │ Verify Pipeline │         │ Sign Dispatcher│
│ (FR-003,014) │       │ (FR-002, 016,   │         │ (FR-001, 008)  │
│              │       │  012, 015, 017) │         │                │
└──────┬───────┘       └────────┬────────┘         └────────┬───────┘
       │                        │                           │
       ▼                        ▼                           ▼
┌──────────────┐       ┌─────────────────┐         ┌────────────────┐
│ Rotation     │       │ Revocation      │         │ Backend        │
│ Manager      │ ◄─────┤ Cache           │         │ Registry       │
│ (FR-005, 013)│       │ (FR-006, 007)   │         │ (FR-008..010)  │
└──────────────┘       └─────────────────┘         └────────┬───────┘
                                                            │
                                                            ▼
                              ┌────────────────────────────────────────┐
                              │ backends/{software,oskeychain,awskms,  │
                              │   yubikey,pkcs11}                      │
                              └────────────────────────────────────────┘

       Every accept / reject / config-change emits exactly one event into
       core/event/ via the audit mapper (FR-011, NFR-005, C-005).
```

### 4.1 Anchor store

- Persisted via `core/storage/` SQLite (storage-foundations) in tables
  `trust_anchors` and `trust_anchor_history`.
- Precedence rules (FR-003): pinned_peer > org_identifier > raw_public_key.
  Pinned overrides looser org membership for the *same* peer id.
- Identity-collision detection (FR-015): a `(agent_id, public_key_fingerprint)`
  unique index plus an in-memory cache; second collision is rejected.
- Pre-flight (FR-014): `Preflight()` walks every configured anchor, calls
  `SupportedAlgorithms` on the dispatch backend, returns structured findings.

### 4.2 Verify pipeline (single hot path)

Order matters — fail-fast cheap checks first:

1. Envelope shape (A2A v1 conformance) — reject `signature_invalid` if shape
   wrong.
2. Algorithm policy gate (FR-004) — reject `algorithm_not_permitted` if alg
   not in operator allow-list.
3. Chain-depth gate — reject `chain_depth_exceeded` (defense-in-depth, edge
   case in spec).
4. Anchor lookup — reject `anchor_missing` / `anchor_removed` (distinct codes
   per edge case in spec).
5. Revocation cache — reject `key_revoked`.
6. Clock-skew window (FR-016) — reject `clock_skew_exceeded`.
7. Signature math — reject `signature_invalid` on any failure.
8. Rotation overlap check (FR-013) — accept with `cache_state = grace` when
   verifying against a previous-but-still-in-overlap key; emit a typed audit
   event so the operator can see who is lagging.
9. Identity-collision check (FR-015) — reject `identity_collision` if a
   different public key was previously associated with the same `agent_id`.
10. Emit audit (`verification-accepted` or `verification-rejected`).
11. Return `VerificationResult`.

NFR-001 budget: every step except signature math is constant-time table or hash
lookup; total non-math overhead targeted < 5 ms p95 on a developer laptop.

### 4.3 Sign dispatcher

- Resolves `IdentityRef.Backend` to a registered `SigningBackend`.
- Calls `backend.Health(ctx)`; if `unavailable`, fails closed (NFR-007) with a
  typed error and emits a `backend-unavailable` audit event. Does **not** fall
  back to `software` even if a cached key would work.
- Calls `backend.Sign(ctx, ref, alg, payload)`.
- Wraps the result in the A2A v1 Signed Agent Cards envelope.

### 4.4 Rotation / overlap

- `BeginRotation(anchorID, newKey, overlap)` writes a row to
  `trust_anchor_history` and stamps the anchor with `previous_key`,
  `overlap_ends`. Emits `key-rotated` event.
- During the window, verification accepts either the previous or the new key,
  but every acceptance against `previous_key` flips `CacheState=grace` and is
  recorded — operator dashboards can show "N peers still on old key."
- `CompleteRotation(anchorID)` purges `previous_key` (or it auto-purges on
  `overlap_ends` lapse).

### 4.5 Revocation cache

- In-memory map keyed by (subject_kind, subject_id) with `effective_at`.
- Backed by SQLite table `trust_revocations`.
- v1.0 ingestion path: operator calls `IngestRevocation` (CLI, RPC, or
  scheduled pull from a configured URL). Default poll interval 60 s
  (Open Question 1 default).
- Acceptance rule: if a record exists for the signing key id *or* its
  identity, with `effective_at <= now`, reject as `key_revoked`. Records apply
  to the *identity*, not just future-dated signatures (acceptance scenario in
  US4).

---

## 5. Data Model (References to FRs)

> Full schema lands in `data-model.md` (Phase 1). Highlights here.

### 5.1 Persistent tables (in storage-foundations SQLite)

| Table | Columns (essence) | Purpose | FRs |
|---|---|---|---|
| `trust_anchors` | `anchor_id PK`, `kind`, `public_key`, `algorithm`, `org_id NULL`, `peer_id NULL`, `installed_at`, `installed_by`, `previous_key NULL`, `overlap_ends NULL`, `state` | Source of truth for what we trust. | FR-003, FR-005, FR-013, FR-015 |
| `trust_anchor_history` | `id PK`, `anchor_id FK`, `change_kind`, `payload`, `applied_at`, `applied_by`, `content_hash` | Audit-friendly per-anchor history; survives deletion. | FR-011, C-003 |
| `trust_revocations` | `revocation_id PK`, `subject_kind`, `subject_id`, `effective_at`, `reason`, `issued_by`, `signature_blob`, `ingested_at` | Revocation cache, forward and back. | FR-006, FR-007 |
| `trust_identities` | `agent_id PK`, `public_key_fingerprint`, `first_seen_at`, `anchor_id FK` | Identity-collision detection. | FR-015 |
| `trust_backend_health` | `backend_kind PK`, `last_status`, `last_checked_at` | Surface backend availability for `harness trust status`. | NFR-007 |

### 5.2 Audit event kinds (emitted into `core/event/`)

Following the emitter-namespace convention from event-log FR-017
(`trust/`):

| Event kind | When | FRs |
|---|---|---|
| `trust/verification-accepted` | `Verify` returns `accepted` | FR-002, FR-011 |
| `trust/verification-rejected` | `Verify` returns `rejected` (carries `rejection_code`) | FR-002, FR-011, FR-017 |
| `trust/anchor-installed` | `InstallAnchor` succeeds | FR-003, FR-011 |
| `trust/anchor-removed` | `RemoveAnchor` succeeds | FR-003, FR-011 |
| `trust/key-rotated` | `BeginRotation` / `CompleteRotation` | FR-005, FR-011, FR-013 |
| `trust/revocation-ingested` | `IngestRevocation` succeeds | FR-006, FR-011 |
| `trust/backend-unavailable` | Backend health flip → unavailable | NFR-007, FR-011 |
| `trust/preflight-finding` | Preflight surfaces a structured warning/error | FR-014 |

Every payload is shaped to enable offline replay of the decision (SC-006):
`anchor_id`, `algorithm`, `cache_state`, `rejection_code` (when applicable),
`backend_kind`, `payload_hash` (NOT the payload), `result_timestamp`. No
private-key bytes. No raw payload bytes (the verifier holds those for the
caller; the audit stores only a hash for forensic correlation).

### 5.3 Wire / envelope

Conforms to A2A v1.0 *Signed Agent Cards* envelope per C-004. Envelope fields
(adapted to A2A's published shape — exact field names tracked from `a2a-go`):

- `payload`: canonicalized AgentCard JSON.
- `signature`: backend-produced bytes.
- `algorithm`: `ed25519` | `ecdsa-p256` | `rsa-pss-sha256`.
- `key_id`: fingerprint of the signing public key.
- `issued_at`, `expires_at` (used with FR-016 clock-skew tolerance).
- `chain` (optional): ordered list of intermediate certs/anchor refs.
- `key_distribution_hint` (optional): URL where the verifier *may* fetch the
  current public key — never used as authority, only as freshness signal for
  rotation pickup.

> If A2A v1.x publishes a more formal authorization scheme before this mission
> ships, the envelope adapter follows the spec rather than diverging (D7 of
> acp-orchestration research).

---

## 6. Integration Points

### 6.1 A2A v1 envelope

- **SDK**: Reuse `github.com/a2aproject/a2a-go` for AgentCard types and
  envelope marshaling where the SDK exposes them. Wrap inside
  `core/trust/envelope.go` so no other `core/` package transitively imports
  the SDK (DIRECTIVE_001 + acp-orchestration D3).
- Where `a2a-go` does not yet expose Signed Agent Cards primitives (D7 of the
  acp research notes the spec is partly emerging), implement the envelope
  ourselves *exactly* per the published A2A v1.0 spec. Emit an ADR
  (`adr-trust-004-envelope-implementation-source`) recording which fields
  came from the SDK and which we hand-rolled, so the diff reverts cleanly
  when the SDK catches up.

### 6.2 Secrets / signing-backend dispatch

Decisions imported from `secrets-keychain-01KQ1A3M/research.md`:

| Backend | Library (per secrets research) | Notes |
|---|---|---|
| `oskeychain` | `zalando/go-keyring` (D1) | Default. Linux fallback chain: Secret Service → XDG portal → fail (D2). |
| `awskms` | `aws-sdk-go-v2/service/kms` + AWS Encryption SDK for Go (D4) | OSS behind `kms_aws` build tag. |
| `yubikey` | `go-piv/piv-go` v2.6.0 (D5) | Pure-Go, no CGo on macOS/Windows; libpcsclite on Linux. |
| `software` | stdlib `crypto/ed25519` etc. | Test-only; gated behind a build tag, never compiled into release binaries. |
| `pkcs11` | `miekg/pkcs11` (D5: deferred) | Interface stub now; v2 implementation. |

Key-material handling reuses `core/secrets/` `Secret` type (secrets-keychain
D6/D7 — `[]byte`-typed, explicit zero, `runtime.KeepAlive`). Public keys live
in plaintext (they're public by definition); private keys never cross the
backend boundary.

> **No re-litigation**: this plan adopts the secrets-keychain mission's
> decisions verbatim. Any change to backend libraries goes through that
> mission's amendment process, not here.

### 6.3 Event log

- `core/trust/audit.go` is the only place trust events are emitted.
- Uses the same redaction-pipeline (event-log FR-005, FR-006) — though by
  construction, trust audit payloads carry no credential-shaped material.
- Hash-chain integrity (event-log FR-004) gives the trust audit log
  cryptographic tamper-evidence on top of append-only API enforcement.
- Trust events are queried by `harness trust status`, `harness log query
  --emitter trust/`, and the future replay UI.

### 6.4 Bundle resolver, shared context, policy engine

These consumers call `TrustEngine.Verify` with their own payload shape (a
bundle manifest, a context pack manifest, a policy artifact). The verification
contract is uniform per FR-012; consumers do not learn anchor or backend
internals. This is what makes the SC-007 stability claim meaningful.

### 6.5 Pre-flight

`core/config/` calls `TrustEngine.Preflight()` during harness startup, *after*
secrets pre-flight. Failures surface with structured findings — anchor id,
backend kind, finding code (`backend_unavailable`, `anchor_algorithm_unsupported`,
`anchor_key_malformed`, `revocation_endpoint_unreachable`). Exit code is
non-zero on `severity=error`. This honors FR-014 + the "edge cases" item about
pre-flight surfacing misconfiguration.

---

## 7. Phasing

### v1.0 — initial release

- Default algorithm: **Ed25519 only** (Open Question 2 default).
- Default backend: `oskeychain` (FR-009).
- Optional backend (build tag, OSS): `awskms` (FR-010, C-006).
- `yubikey` interface stub; `pkcs11` interface only.
- Manual revocation pull (FR-007 default — operator-configured URL, 60-second
  poll). No automatic distribution.
- A2A v1.0 Signed Agent Cards envelope conformance.
- Full audit emission for every event kind in §5.2.
- Pre-flight validation (FR-014) wired through `core/config/`.
- Cross-platform parity tests for `oskeychain` on macOS / Linux / Windows
  (NFR-008).
- **Deferred from v1.0**: A2A cross-org formal authorization scheme (D7 of
  acp research — A2A spec still emerging); automatic revocation distribution;
  ECDSA / RSA-PSS opt-ins; YubiKey full impl; HSM / PKCS#11.

### v1.x — incremental hardening

- ECDSA-P256 and RSA-PSS algorithm opt-ins (FR-004).
- Full `yubikey` backend impl (D5 of secrets research).
- Automatic revocation distribution mechanism (push-based or short-lived
  signature TTL — design decision deferred until A2A trust roadmap firms up).
- Operator UI surfaces for trust status, anchor management, rotation
  dashboards.
- Cross-instance integration tests in CI.

### v2 — full ecosystem alignment

- HSM / PKCS#11 backend impl (D5 deferred).
- Full A2A trust scheme conformance once that part of the spec is published
  formally (currently noted as "still being formalized" in acp research).
- Cross-org context-pack signing (depends on `shared-context-distribution`
  v2).
- Cloud KMS expansion to Azure Key Vault and Google Cloud KMS (per
  secrets-keychain Open Question 3).

---

## 8. Risk Register

| # | Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|---|
| R-001 | A2A v1 Signed Agent Cards envelope drifts between our v1.0 ship and a future spec revision (D7 of acp research notes auth scheme is "still being formalized"). | Medium | High | Wrap A2A SDK behind `core/trust/envelope.go`. Track A2A spec quarterly. Plan v1.x and v2 explicitly assume envelope evolution. ADR (`adr-trust-004`) records what was hand-rolled vs SDK-sourced. |
| R-002 | OS keychain quirks across platforms — macOS first-access prompt in headless mode, Linux Secret Service unavailable on ssh-only boxes, Windows Cred Manager character limits. | High | Medium | Adopt secrets-keychain D2 (Secret Service → XDG portal → explicit file backend, no silent fallback). Pre-flight (FR-014) surfaces unreachable backends *before* first use. UX hint in macOS first-run. |
| R-003 | Key-loss recovery — operator loses the OS-keychain entry holding their org signing key with no backup. | Medium | High | Document recommended posture: enterprise operators use `awskms` (which is recoverable through cloud IAM); OSS operators are advised to hold a sealed backup of public/private key out-of-band. Plan an explicit "lost key" runbook in v1.x. |
| R-004 | Revocation propagation gap — manual pull (FR-007 default) means a compromised key may stay accepted for up to 60 s after revocation. | Medium | Medium | Document propagation budget clearly (NFR-003 = 5 min p95 against the v1 mechanism). Operators with stricter SLAs configure shorter poll intervals or short-lived signature TTLs. Automatic distribution lands in v1.x. |
| R-005 | Audit-log volume — every verification (potentially per request) emits an event; high-traffic peers blow up the event log. | Medium | Medium | Sampling is not appropriate for audit; instead, the event log's retention policy (event-log FR-013) and storage-foundations indexing handle volume. Hot-path verification cache (in-memory, fingerprint → recent-decision) keeps math cheap; the *audit emission* still happens (NFR-005 demands 100% coverage). Document expected volume; plan a follow-up if it becomes a real problem. |
| R-006 | Algorithm policy too strict — operators with regulatory ECDSA / RSA-PSS requirements can't ship until v1.x. | Low | Medium | Default Ed25519-only matches what most modern stacks accept. Operators in regulated environments can pre-stage their algorithm requirement as a v1.x scheduling input. ECDSA-P256 + RSA-PSS algorithm slots are on the interface from v1.0 — only the operator-facing config flag is gated. |
| R-007 | Identity collision is over-eager — two operators legitimately reset their key on the same `agent_id` but the harness rejects the *legitimate* second one. | Low | Medium | Collision rejection is on `(agent_id, fingerprint)`; legitimate rotation routes through `BeginRotation` rather than fresh-installing a new key. Operator-facing tooling clearly separates "rotate" from "install." |
| R-008 | KMS / HSM backend SDK pulls heavyweight transitive deps into OSS binary even when build tag is off. | Low | Low | Each backend lives in its own subpackage; import is gated by build tag (`//go:build kms_aws`). CI matrix builds OSS tag-off and verifies binary size + dependency tree. |
| R-009 | Charter drift — `core/trust/` accidentally pulls in a backend SDK dependency on the main package (fails DIRECTIVE_001 / C-001). | Low | High | Explicit lint check via `golangci-lint` `depguard` rule: `core/trust/` (top level) may not import `aws-sdk-go-v2`, `go-piv`, `zalando/go-keyring`, `miekg/pkcs11`. Backends are the only allowed importers. |
| R-010 | Spec-vs-A2A-roadmap drift — A2A formalizes a different trust posture (e.g., switches to JWS over signed JSON) before our v1.0 ships. | Low | High | Quarterly check-in with A2A LF AI & Data working group output. ADR cadence captures every envelope-shape decision so reverts are cheap. v2 phase explicitly carries "full A2A trust formalization" as in-scope. |

---

## 9. Open Questions for the User

The spec has three explicit `[NEEDS CLARIFICATION]` items. Working defaults
shown; please confirm or override before tasks generation.

1. **Revocation distribution (FR-007)** — *Default:* manual pull from a
   configured URL with 60-second poll interval. Full CRL/OCSP-style automation
   deferred to v1.x.
   - **Confirm?** [ ] yes / [ ] override
   - If override, please specify the desired v1 mechanism.

2. **Default algorithm policy (FR-004)** — *Default:* Ed25519-only at v1.0;
   ECDSA-P256 and RSA-PSS opt-in in v1.x.
   - **Confirm?** [ ] yes / [ ] override
   - If override, please specify which algorithms ship enabled at v1.0.

3. **OSS / enterprise backend split (C-006)** — *Default:* HSM and cloud-KMS
   backends ship in OSS behind build tags (off by default), so the OSS /
   enterprise difference is licensing/support, not a fork.
   - **Confirm?** [ ] yes / [ ] override
   - If override, specify whether KMS / HSM should be enterprise-only at the
     binary level.

> Answers feed `adr-trust-001`, `adr-trust-002`, `adr-trust-003` (all queued
> per DIRECTIVE_003).

---

## 10. Charter Re-check (post-design)

| Gate | Status |
|---|---|
| DIRECTIVE_001 boundary | **PASS** — `core/trust/` is the only entry; backends in their own subpackages; lint rule queued in §8 R-009 mitigation. |
| DIRECTIVE_003 ADRs | **PASS** — four ADRs queued: default algorithm, OSS/enterprise split, revocation distribution, envelope implementation source. |
| DIRECTIVE_010 spec faithfulness | **PASS** — all 17 FRs and 8 NFRs mapped to a section above. Three NEEDS CLARIFICATION items surfaced in §9. |
| Charter local-first | **PASS** — verification is local; backend SDKs are opt-in; manual revocation pull is opt-in. |
| Charter security-first | **PASS** — fail-closed on backend unavailability (NFR-007), no plaintext keys in any persisted state (C-002), append-only audit (C-003). |
| SOC 2 readiness (C-005) | **PASS** — every decision and config change emits exactly one audit event with fields sufficient for offline replay. |

No charter violations to escalate. Ready for `/spec-kitty.tasks` once the
three Open Questions are resolved.

---

## 11. Outputs Produced by This Plan

- `kitty-specs/a2a-signed-cards-trust-01KQ18P9/plan.md` — this document.
- `kitty-specs/a2a-signed-cards-trust-01KQ18P9/data-model.md` — pending Phase 1
  artifact (table-by-table SQL DDL + envelope JSON shape).
- `kitty-specs/a2a-signed-cards-trust-01KQ18P9/research.md` — to be drafted only
  if user-facing Open Questions surface new unknowns; v1 defaults are captured
  inline in this plan.
- `kitty-specs/a2a-signed-cards-trust-01KQ18P9/contracts/` — to land in Phase 1:
  the Go interface signatures from §3 split into `trust.go.contract` and
  `backend.go.contract` for downstream consumers to depend on while
  implementation proceeds.
- `kitty-specs/a2a-signed-cards-trust-01KQ18P9/quickstart.md` — Phase 1: a
  five-minute "configure an org anchor, sign a card, verify it on a peer"
  walkthrough mirroring SC-001.

---

## 12. Branch Contract (final restatement)

> Plan was authored on the project root checkout, currently `main`. The
> mission's `target_branch` per `meta.json` is `main`. All implementation
> work lands on `feat/a2a-signed-cards-trust-01KQ18P9` (per charter Branch
> Strategy + spec.md feature branch field). The PR squash-merges to `main`
> with at least one maintainer approval. No direct pushes to `main`.
> Material design decisions ship with ADRs under `docs/adr/` per
> DIRECTIVE_003.

**Next command** (user-invoked, not by this plan): `/spec-kitty.tasks` —
once the three Open Questions in §9 are resolved.
