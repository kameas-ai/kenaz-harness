# Feature Specification: Secrets and OS Keychain Abstraction

**Feature Branch**: `feat/secrets-keychain-01KQ1A3M`
**Created**: 2026-04-25
**Status**: Draft
**Input**: Foundation mission. Defines the harness's cross-platform secret-handling layer: indirect credential references, OS-native secure storage, in-memory secret hygiene, and a stable contract for non-keychain backends (cloud KMS, HSM, file-based for CI). Multiple drafted specs reference this as an assumed shared surface.

## Why this mission exists

`llm-connector` FR-003 (auth references only — env / keychain / file / aws_profile), `a2a-signed-cards-trust` FR-008–FR-009 (signing-backend abstraction with OS keychain default), `shared-context-distribution` FR-009 (scoped pack credential refs), `bundle-format-resolver` (channel auth credential refs), and `storage-foundations` (database encryption key reference) all consume the same indirect-credential machinery. Without it specified once, each of those missions has to redefine the contract.

## Dependencies and relationships

- **Blocks**: every mission that resolves credentials at runtime — `llm-connector`, `a2a-signed-cards-trust`, `shared-context-distribution`, `bundle-format-resolver`, `storage-foundations`.
- **Adjacent**: `event-log` (records credential-resolution events; redaction depends on never seeing the resolved value).
- **Reuses**: charter security-first invariant; SOC 2-readiness.
- **Does not cover**: the credential lifecycle of the *operator's* identity (user accounts) — that belongs to a future identity / account model mission.

## User Scenarios & Testing *(mandatory)*

### User Story 1 — Configuration files only carry references, never plaintext credentials (Priority: P1)

Bundle configuration, lockfiles, RPC payloads, and any operator-edited config carry credential *references* — pointers to where the credential lives — never the credential value itself. Resolution happens at use time and the resolved value never enters configuration, the event log, or process arguments.

**Why this priority**: This is the charter's hard security invariant. Every drafted spec depends on it.

**Independent Test**: An automated audit suite scans all configuration sources, the event log, process arguments, and on-disk swap-monitored state after a session and confirms zero plaintext credentials.

**Acceptance Scenarios**:

1. **Given** a bundle declares a provider with `auth: { keychain: "anthropic-api-key" }`, **When** the bundle is read and persisted to the lockfile, **Then** the keychain *reference* is recorded; the resolved value never enters the lockfile or any config file.
2. **Given** an attempt to put an inline plaintext credential in any configuration source, **When** the harness loads it, **Then** load fails with an actionable "inline credentials not permitted" error.

---

### User Story 2 — OS-native secure storage works across macOS, Linux, and Windows (Priority: P1)

The default credential backend is the platform's native secure storage: Keychain on macOS, Credential Manager on Windows, Secret Service or kernel keyring on Linux. The same configuration shape works on all three. Credentials added on one machine are not portable (correct: keys stay where they were stored), but the *reference* shape and the harness behavior are identical across platforms.

**Why this priority**: Cross-platform parity is a charter constraint. A keychain story that works only on macOS is unshippable.

**Independent Test**: Run identical harness configs on each of macOS, Linux, and Windows. Each correctly resolves credentials from its native store; references are platform-portable, values are not (by design).

**Acceptance Scenarios**:

1. **Given** a credential is stored in the OS keychain, **When** the harness resolves a reference to it, **Then** resolution succeeds on every supported platform with identical behavior.
2. **Given** a referenced credential is missing on the current platform, **When** resolution is attempted, **Then** the harness returns a typed "credential not found" error identifying the reference, not a generic platform error.

---

### User Story 3 — Multiple backend kinds are supported through one stable contract (Priority: P1)

Beyond OS keychain, operators may resolve credentials from environment variables (CI/CD), encrypted files (containers), AWS / Azure / GCP secret services (cloud-deployed enterprise), HSM (regulated environments). All speak the same backend contract: given a reference, return a credential or a typed error. Adding a new backend does not require changes to consumers.

**Why this priority**: Different deployments have different policies — desktop operators want OS keychain; CI wants env; enterprise wants KMS. Forcing one backend forces operators to fork.

**Independent Test**: A test backend is implemented in its own package, registered, and used to resolve a credential — without any commit to consuming packages.

**Acceptance Scenarios**:

1. **Given** an operator configures multiple backends, **When** a reference matches a backend kind, **Then** the corresponding backend is dispatched.
2. **Given** an attempted change that requires modifying a shared consumer interface to add a new backend, **When** reviewed, **Then** the architectural-integrity check flags it.

---

### User Story 4 — Resolved credentials are short-lived in process memory (Priority: P2)

When the harness resolves a credential, the bytes live in memory only as long as the operation that needs them. After use, the bytes are explicitly zeroed where the runtime allows. Resolved values are never logged, never written to disk by the harness itself, and never serialized into RPC payloads beyond the immediate consumer.

**Why this priority**: Defense-in-depth. Combined with redaction (event log) and indirect references (config), this minimizes the attack surface for memory dumps and crash logs.

**Independent Test**: After a session, scan the harness's heap snapshot (where the runtime supports it) for the resolved credential bytes. Zero matches at the boundary the contract guarantees.

**Acceptance Scenarios**:

1. **Given** a credential is resolved for an LLM call, **When** the call completes, **Then** the resolved bytes are zeroed and not retained beyond the call's scope.
2. **Given** an unhandled error occurs during use, **When** the harness panics or recovers, **Then** the credential bytes do not appear in any persisted error report.

---

### User Story 5 — Pre-flight validation catches misconfiguration before first use (Priority: P2)

At startup, the harness validates every configured credential reference: the backend exists, the entry is reachable, and (for backends that support it) the entry is non-empty. Failures surface at startup with a precise reference id, not as a runtime "your model call mysteriously failed."

**Why this priority**: Configuration errors at midnight on an operator's first try are the worst kind of failure mode; pre-flight makes them obvious.

**Independent Test**: Configure a reference whose entry is missing. The harness fails to start with a structured error naming the reference; no model call is attempted.

**Acceptance Scenarios**:

1. **Given** a reference whose backend or entry is missing, **When** the harness performs pre-flight, **Then** the operator sees a single clear startup error per missing reference.
2. **Given** all references resolve at pre-flight, **When** the harness operates, **Then** later resolutions hit warm cache or re-resolve with the same backend.

---

### User Story 6 — Credentials are scoped to specific consumers (Priority: P3)

The same harness installation may use one credential for one provider and a different credential for another, or different credentials per workflow. The reference syntax disambiguates. An operator can audit which consumers used which references over a time window.

**Why this priority**: Multi-tenant or multi-account workflows are common. P3 because the v1 default of "one reference per declared consumer" already handles most cases — granular per-consumer auditing is a nice-to-have.

**Independent Test**: Two consumers use different references; the audit log distinguishes them.

**Acceptance Scenarios**:

1. **Given** two providers reference different credentials, **When** the harness operates, **Then** each resolution event names which consumer requested which reference.

---

### Edge Cases

- The OS keychain prompts the user for permission on first access (macOS): the harness presents a clear UX hint rather than failing silently in headless mode; an operator can pre-authorize where the platform allows.
- The Linux secret-storage daemon (gnome-keyring / KWallet) is not running: fall back to kernel keyring with a recorded warning, or fail closed if neither is available, depending on operator policy.
- The credential backend is temporarily unreachable (KMS API outage): retry per a configurable budget, then fail closed with a backend-status report.
- The credential value contains the entire keychain (an HSM PIN that protects further keys): never resolve through this layer; HSM PINs use the HSM's own protocol and live in a more constrained backend.
- A credential reference syntax is misspelled in config: pre-flight rejects with the exact reference and a hint.
- An operator rotates the underlying credential while the harness holds it cached: the cache is invalidated within a configurable TTL; rotated credentials take effect on the next resolution.

## Requirements *(mandatory)*

### Functional Requirements

| ID | Title | User Story | Priority | Status |
|----|-------|------------|----------|--------|
| FR-001 | Indirect-reference syntax | As an author, I want a stable reference syntax (`{ env: "VAR" }`, `{ keychain: "entry" }`, `{ file: "/path" }`, `{ aws_profile: "name" }`, `{ kms: "arn" }`) used uniformly by every consumer. | High | Open |
| FR-002 | Backend abstraction | As a contributor, I want a stable backend contract (`resolve(ref) -> bytes \| error`, `health()`, `kind()`) so backends are addable in their own packages. | High | Open |
| FR-003 | OS keychain backend (default) | As an operator, I want a cross-platform OS keychain backend covering macOS Keychain, Windows Credential Manager, and Linux Secret Service / kernel keyring. | High | Open |
| FR-004 | Environment-variable backend | As a CI operator, I want an env-backend for headless deployments. | High | Open |
| FR-005 | File backend | As a container operator, I want a file backend for mounted secret files (e.g., Kubernetes secrets, Docker secrets). | High | Open |
| FR-006 | AWS profile backend | As a Bedrock user, I want an AWS profile backend that resolves through the standard AWS credential chain. | High | Open |
| FR-007 | Cloud KMS backend (optional) | As an enterprise operator, I want an opt-in KMS backend (AWS KMS / Azure Key Vault / Google KMS) that returns short-lived credentials. | Medium | Open |
| FR-008 | HSM backend (optional, enterprise) | As a regulated operator, I want an HSM backend; this typically lives in the commercial enterprise build. | Medium | Open |
| FR-009 | Pre-flight validation | As an operator, I want every configured reference validated at startup; failures surface immediately with the reference id. | High | Open |
| FR-010 | Resolution cache with TTL | As an operator, I want resolved credentials cached briefly to avoid backend round-trips on hot paths; TTL is configurable per backend. | Medium | Open |
| FR-011 | Cache invalidation on rotation | As an operator, I want the cache invalidated within a configurable TTL so rotation takes effect promptly. | High | Open |
| FR-012 | Credential-resolution events | As an operator, I want every resolution attempt recorded in the event log: which consumer, which reference, which backend, success/failure, latency — with the resolved value never present. | High | Open |
| FR-013 | Zeroize after use | As a contributor, I want a documented pattern for receiving a credential, using it, and explicitly zeroing the byte slice. | High | Open |
| FR-014 | Error taxonomy | As an operator, I want a stable error taxonomy: backend-unavailable, reference-not-found, reference-empty, permission-denied, format-invalid, rotated-mid-use. | High | Open |
| FR-015 | Refusal of inline plaintext | As an operator, I want any attempt to inline a plaintext credential rejected at config-load time. | High | Open |
| FR-016 | Per-reference scoping | As an operator, I want each consumer to declare which reference it uses so audit can attribute resolutions. | Medium | Open |
| FR-017 | Backend health probe | As an operator, I want a health probe per backend exposed via `harness secrets status`. | Medium | Open |

### Non-Functional Requirements

| ID | Title | Requirement | Category | Priority | Status |
|----|-------|-------------|----------|----------|--------|
| NFR-001 | Resolution latency (warm cache) | Resolution from warm cache adds under 1 ms p99 to the consumer call. | Performance | High | Open |
| NFR-002 | Resolution latency (cold OS keychain) | Cold OS-keychain resolution completes in under 50 ms p95 on a developer laptop. | Performance | High | Open |
| NFR-003 | Plaintext leakage | Resolved credential bytes appearing in event log, configuration, lockfile, RPC payloads, error reports, or temp files: zero across the audit matrix. | Security | High | Open |
| NFR-004 | Cross-platform parity | Identical reference shapes resolve identically across macOS, Linux, Windows. | Portability | High | Open |
| NFR-005 | Backend extensibility | Adding a new backend requires no changes to consumer packages. | Maintainability | High | Open |
| NFR-006 | Pre-flight completeness | 100 % of configured references are validated at startup; unresolvable references surface within startup latency budget. | Reliability | High | Open |
| NFR-007 | Rotation propagation | An operator-rotated credential takes effect within the configured TTL across all consumers within 5× TTL p99. | Reliability | Medium | Open |

### Constraints

| ID | Title | Constraint | Category | Priority | Status |
|----|-------|------------|----------|----------|--------|
| C-001 | Architectural integrity | Secrets logic lives in `core/secrets/`. Backends live in their own subpackages. No other `core/` package imports a backend SDK directly. | Technical | High | Open |
| C-002 | No plaintext credentials in any persisted state | Configuration sources, lockfiles, RPC payloads, event log, error reports never carry resolved credential values. | Security | High | Open |
| C-003 | Append-only event log immutability | Resolution and rotation events are append-only. | Security | High | Open |
| C-004 | Charter local-first | OS keychain is the default; non-network-required by definition. Network-backed backends are opt-in. | Technical | High | Open |
| C-005 | SOC 2 readiness | Resolution events, rotation events, and pre-flight outcomes produce evidence sufficient for SOC 2 audit per the charter. | Regulatory | High | Open |
| C-006 | Fail-closed | Backend unavailability surfaces as a typed error; the harness never falls back to a less-secure backend without explicit operator opt-in. | Security | High | Open |

### Key Entities

- **Credential Reference**: a declarative pointer (`{ kind: env|keychain|file|aws_profile|kms|hsm, locator: <kind-specific> }`) that resolves to a credential at use time. Stored in configuration, lockfiles, and RPC payloads in place of the credential itself.
- **Backend**: a pluggable resolver for one reference kind. Contract: `resolve(ref) -> bytes`, `health() -> ok|degraded|unavailable`, `kind() -> string`. Backends live in their own packages.
- **Resolved Credential**: a short-lived in-memory byte slice. Lifetime is bounded by the consumer call. Explicit zeroize on release.
- **Resolution Event**: an append-only event log entry recording one resolution attempt — consumer id, reference id, backend kind, result (ok/error code), latency. Never carries the resolved value.
- **Pre-flight Result**: structured per-reference status produced at startup; fails the startup if any required reference is unresolvable, unless the operator marks it optional.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A new operator can configure their first provider credential through the OS keychain and run a successful generation in under 5 minutes from a clean clone, on each supported platform.
- **SC-002**: Zero plaintext credentials appear in any persisted state across the audit matrix.
- **SC-003**: 100 % of configured references are validated at pre-flight; misconfiguration surfaces as a startup error with the reference id.
- **SC-004**: A new backend is added end-to-end without modifying any consumer package.
- **SC-005**: A rotated credential takes effect within the configured TTL across all consumers, in 99 % of test runs.
- **SC-006**: Resolution from warm cache adds under 1 ms p99 to consumer calls.

## Assumptions

- The charter's local-first invariant remains binding; OS keychain is the default backend, network-backed backends are opt-in.
- Go ecosystem libraries for OS-native secret stores exist and are licensable (e.g., `zalando/go-keyring`, `99designs/keyring`); choice is a planning-phase decision.
- The event log layer (`event-log`) handles redaction so this layer never has to defensively scrub; this layer's responsibility is "never emit the value in the first place."
- Operator-managed credential lifecycle (creation, rotation, revocation in the *upstream* system) is out of scope; this layer consumes whatever is current at resolution time.

## Open Questions

1. **[NEEDS CLARIFICATION]** Default cache TTL — short (30 s) vs longer (5 min)? Default if unresolved: 60 s. Trade-off: shorter TTL means rotation propagates faster but more keychain prompts on macOS where each access may require a confirmation; longer TTL is friendlier but propagates slower.
2. **[NEEDS CLARIFICATION]** Linux fallback chain — Secret Service first, then kernel keyring, then file backend (with operator policy)? Default: Secret Service → kernel keyring → fail (no implicit file fallback unless operator configures it explicitly).
3. **[NEEDS CLARIFICATION]** Process-arg / environment-leak protection — should the harness scrub `os.Args` and `os.Environ` of credential-shaped substrings before any logging or reporting? Default: yes, scrub at startup; document that the env backend's *names* (not values) appear normally, but no value-shaped substrings are emitted.
