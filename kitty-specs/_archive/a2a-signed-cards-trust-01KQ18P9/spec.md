# Feature Specification: A2A Signed Agent Cards and Cross-Instance Trust

**Feature Branch**: `feat/a2a-signed-cards-trust-01KQ18P9`
**Created**: 2026-04-25
**Status**: Draft
**Input**: User description: "Create the follow-up for signed agent cards and overall shared context and its distribution." This mission is the *trust primitive* half of that follow-up — the infrastructure that proves "this card / this context / this agent really comes from the org / team / individual it claims to come from." A companion mission (`shared-context-distribution`) is the content layer that consumes this primitive.

## Dependencies and relationships

- **Depends on**: `acp-orchestration-01KQ17ZK` (A2A core protocol integration). Decision D7 in that mission's `research.md` explicitly deferred signed cards, cross-org trust, and PKI to this follow-up.
- **Enables**: `shared-context-distribution-01KQ18PA` (org / team / personal context packs with signed provenance) and any future mission that needs cryptographic provenance over A2A payloads.
- **Reuses**: the indirect credential-reference machinery specified in `llm-connector-01KQ1770` FR-003 — private signing keys never appear inline.
- **Does not cover**: the content of shared context itself, the distribution transports for context packs, or Anthropic's Agent Client Protocol (that remains deferred).

## User Scenarios & Testing *(mandatory)*

### User Story 1 — An operator whitelists which orgs / teams / identities their harness will trust (Priority: P1)

An operator configures a set of trust anchors (a specific org's signing key, a team key below it, or a specific peer's pinned key). Their harness accepts signed AgentCards issued under those anchors and rejects anything else. The operator does not have to hand-verify fingerprints at every connection; once the anchor is installed, the verification decision is automatic, consistent, and auditable.

**Why this priority**: Without configurable trust anchors, cross-instance and cross-org A2A is either unsafe (accept anyone) or unusable (reject everyone). This is the minimum viable posture for enterprise deployments.

**Independent Test**: Two harness instances configured with a shared org trust anchor can successfully call each other's Skills. A third instance *not* in the trust set is rejected at card verification, before any Skill is invoked.

**Acceptance Scenarios**:

1. **Given** the operator has installed an org trust anchor, **When** a peer presents an AgentCard signed under that anchor, **Then** the card is accepted and the call proceeds.
2. **Given** the operator has installed no anchors, **When** a peer presents any signed card, **Then** the card is rejected with an actionable "no matching trust anchor" error.
3. **Given** the operator has installed an org anchor, **When** a peer presents a card signed by a different org, **Then** the card is rejected before any Skill is invoked and the event log records the rejection with the rejecting anchor id.

---

### User Story 2 — Tampering with a signed AgentCard is detected and rejected (Priority: P1)

An attacker on the network path modifies a signed AgentCard payload mid-transit — changes the endpoint URL, adds a Skill, or substitutes a public key. Verification fails. The harness refuses the card and records the verification failure.

**Why this priority**: A signature that does not detect tampering is theater. The whole value proposition of signing is the integrity guarantee.

**Independent Test**: A fault-injecting harness rewrites one byte in a signed card between fetch and verification — verification fails; the event log records a "signature invalid" rejection; no Skill is invoked and no outbound request is made.

**Acceptance Scenarios**:

1. **Given** a valid signed card, **When** any byte of the covered payload is altered in transit, **Then** verification fails with a typed "signature invalid" error.
2. **Given** a card signed with algorithm A, **When** the harness's configured algorithm policy does not permit A, **Then** verification fails with a typed "algorithm not permitted" error rather than silently accepting a weaker algorithm.

---

### User Story 3 — Key rotation does not break existing peers (Priority: P1)

An operator rotates their org's signing key. Peers with the old public key accept a transition card covering both keys, or pick up the new key through the configured key distribution mechanism. In-flight tasks complete without error. No peer is forced to simultaneously rotate.

**Why this priority**: Long-lived trust anchors without a rotation story are a SOC 2 finding waiting to happen. An enterprise-ready posture requires rotation as a first-class operation, not a rebuild.

**Independent Test**: A test fixture rotates the signing key mid-session. Tasks initiated before rotation complete successfully; tasks initiated after rotation succeed against peers that have picked up the new key; peers that have not yet updated fall back to a grace-period verification against the previous key.

**Acceptance Scenarios**:

1. **Given** a signing key is rotated with a configured overlap window, **When** a peer presents a card signed by either the previous or the new key during the overlap, **Then** verification succeeds and the overlap is recorded in the event log.
2. **Given** the overlap window has expired, **When** a peer presents a card signed by the previous (now-retired) key, **Then** verification fails with a typed "key expired" error.

---

### User Story 4 — A revoked identity is rejected even when its signature is still valid (Priority: P2)

A team member's personal signing key is known-compromised. The operator publishes a revocation. From the moment revocation propagates, no harness in the trust set accepts cards signed by that key, even though the math still checks out.

**Why this priority**: Without revocation, a single compromised key is a total-trust-reset event. For v1, simpler mechanisms (short-lived signatures with short TTLs, manual trust-anchor removal) can cover most operator needs while a full revocation distribution mechanism is designed — hence P2, not P1.

**Independent Test**: A revocation for a specific key id is published and distributed. Every harness that receives it rejects subsequently presented cards signed by that key within a measurable propagation budget.

**Acceptance Scenarios**:

1. **Given** a key has been revoked and the revocation has propagated, **When** a peer presents a card signed by that key, **Then** verification fails with a typed "key revoked" error.
2. **Given** a card was issued *before* revocation timestamp but presented *after*, **When** the harness verifies it, **Then** verification fails — the revocation applies to the *identity*, not just to future-dated signatures.

---

### User Story 5 — Every trust decision is auditable (Priority: P2)

Every accept, every reject, every trust-anchor install, every key rotation, every revocation ingestion, every fallback to grace-period is recorded as an append-only event log entry. An operator or auditor can later answer "at time T, did my harness trust key K? on what basis?"

**Why this priority**: SOC 2 readiness. Also the foundation for post-incident forensics if a compromised anchor is ever identified.

**Independent Test**: A complete trust-configuration and verification sequence is replayed from the event log alone and reproduces the same accept/reject decisions.

**Acceptance Scenarios**:

1. **Given** a verification decision (accept or reject), **When** the event log is queried for that point in time, **Then** an entry exists recording the anchor id used, the algorithm used, the cache state (fresh vs grace vs stale), and the decision.
2. **Given** any trust-configuration change (install, remove, rotate, revoke), **When** the event log is queried, **Then** an entry exists recording the change, the operator or system that made it, and a content hash of the new state.

---

### User Story 6 — Private signing keys never leave secure storage (Priority: P2)

When the harness produces a signed AgentCard on behalf of an operator, the private key material is loaded from OS-level secure storage (Keychain on macOS, Credential Manager on Windows, Secret Service / kernel keyring on Linux), or from a hardware security module where configured. The key never appears in bundle source, event log, process arguments, or on-disk working files.

**Why this priority**: Enterprise posture and SOC 2 readiness both require HSM-or-equivalent key protection for signing identities. This is the on-ramp to both.

**Independent Test**: A security-audit suite inspects all on-disk state, process memory (where testable), command-line arguments, and the event log after a signing operation and confirms the private key bytes appear nowhere outside secure storage.

**Acceptance Scenarios**:

1. **Given** the harness signs an AgentCard, **When** the audit suite scans the full on-disk footprint, **Then** the private key bytes do not appear.
2. **Given** the operator configures an HSM or cloud KMS as the signing backend, **When** the harness signs an AgentCard, **Then** the signing operation is performed via the backend's signing interface and the private key never transits the harness process.

---

### Edge Cases

- A card is signed by an anchor the harness once trusted but has since removed: rejected with "anchor removed" (distinct from "no matching anchor" so audit distinguishes the two).
- Clock skew between issuer and verifier crosses the signature validity window: configurable tolerance applied; beyond tolerance, reject with "clock skew exceeds policy."
- A card presents a chain that is valid but exceeds the configured maximum chain depth: rejected as a defense-in-depth measure against complexity attacks.
- Two peers present cards with the same `agent_id` but different public keys: reject the *second* one encountered with "identity collision," rather than silently overwriting.
- The HSM is temporarily unavailable during a signing request: fail closed (do not fall back to an in-memory copy of the key even if one is cached); operator receives a clear "signing backend unavailable" error.
- A trust anchor is configured with a public key that does not match any supported algorithm: surface the misconfiguration at startup pre-flight, not at first use.
- A signed card references a skill not yet known to the verifier: verification succeeds (skills are content, not identity); use of the skill proceeds per separate authorization policy.

## Requirements *(mandatory)*

### Functional Requirements

| ID | Title | User Story | Priority | Status |
|----|-------|------------|----------|--------|
| FR-001 | Sign outbound AgentCards | As an operator, I want the harness to cryptographically sign every outbound AgentCard it publishes so remote peers can verify origin and integrity. | High | Open |
| FR-002 | Verify inbound AgentCards | As an operator, I want every inbound AgentCard verified against my configured trust anchors before any Skill call is accepted. | High | Open |
| FR-003 | Trust anchor configuration | As an operator, I want to declare trust anchors in configuration (by public key, by org identifier, or by pinned peer id), with clear precedence rules. | High | Open |
| FR-004 | Algorithm policy | As an operator, I want to configure which signing algorithms are acceptable (Ed25519 minimum, ECDSA-P256 and RSA-PSS permitted where required), and have weaker algorithms rejected at policy rather than at implementation. | High | Open |
| FR-005 | Key rotation with overlap window | As an operator, I want to rotate signing keys with a configurable overlap window during which both old and new signatures verify, so that rotations do not require synchronized peer updates. | High | Open |
| FR-006 | Revocation ingestion | As an operator, I want to ingest revocation records (by key id or identity) and have them take effect across all verification decisions within a measurable propagation budget. | High | Open |
| FR-007 | Revocation distribution placeholder | As an operator, I want a clear v1 revocation mechanism (even if it is "manually distributed short-lived signatures" rather than a full CRL/OCSP) so that compromised keys can be contained without rebuilding trust. | Medium | Open |
| FR-008 | Signing backend abstraction | As a contributor, I want a stable signing-backend contract (in-process software keys, OS keychain, cloud KMS, HSM) so backends can be added without modifying `core/` outside the backend's own package. | High | Open |
| FR-009 | OS keychain backend | As an operator on macOS / Linux / Windows, I want the harness to use OS-native secure storage (Keychain / Secret Service or kernel keyring / Credential Manager) for signing keys by default. | High | Open |
| FR-010 | Cloud KMS / HSM backend (optional) | As an enterprise operator, I want an opt-in signing backend for cloud KMS or HSM so private keys never leave the managed boundary. | Medium | Open |
| FR-011 | Append-only trust audit events | As an operator, I want every verification decision, trust-anchor change, rotation, and revocation ingestion recorded in the harness append-only event log with enough detail for SOC 2 audit. | High | Open |
| FR-012 | Verification API for adapters | As a contributor, I want a stable verification API (given: payload, signature envelope, policy) that A2A, shared-context, and any future signed surface can call uniformly. | High | Open |
| FR-013 | Grace-period fallback for rotation | As an operator, I want a configurable grace-period during rotation where the previous key is accepted but each acceptance is flagged in the event log so I can see when peers have caught up. | Medium | Open |
| FR-014 | Pre-flight trust configuration validation | As an operator, I want configured trust anchors and backends validated at harness startup so misconfiguration surfaces before the first connection rather than at first use. | High | Open |
| FR-015 | Identity collision detection | As an operator, I want the harness to detect when two peers claim the same `agent_id` with different public keys and reject the later one rather than silently overwrite the trust cache. | Medium | Open |
| FR-016 | Clock-skew tolerance with policy | As an operator, I want a configurable clock-skew tolerance applied to signature validity windows, with rejections beyond tolerance surfaced clearly rather than treated as generic verification failures. | Medium | Open |
| FR-017 | Rejection reason taxonomy | As an operator, I want a stable taxonomy of rejection reasons (signature invalid, algorithm not permitted, anchor missing, anchor removed, key revoked, key expired, identity collision, clock skew) so I can alert and filter on specific failure modes. | High | Open |

### Non-Functional Requirements

| ID | Title | Requirement | Category | Priority | Status |
|----|-------|-------------|----------|----------|--------|
| NFR-001 | Verification latency | Card verification adds under 5 ms p95 overhead to a new-peer handshake (excluding signature math on hot-path caches, which are additionally bounded). | Performance | High | Open |
| NFR-002 | Signing latency | Producing a signed AgentCard takes under 20 ms p95 with a software keychain backend; HSM / cloud KMS latency is bounded by backend SLA and surfaced to the operator. | Performance | Medium | Open |
| NFR-003 | Revocation propagation | A published revocation is observed and enforced within 5 minutes p95 across connected peers using the v1 distribution mechanism. | Reliability | High | Open |
| NFR-004 | Zero private-key disk leakage | Private signing key bytes do not appear in bundle source, event log, process arguments, swap files monitored by the audit suite, or temporary on-disk state, across the full platform matrix. | Security | High | Open |
| NFR-005 | Audit completeness | 100 % of verification decisions and trust-config changes produce append-only event log entries with anchor id, algorithm, decision, and timestamp. | Auditability | High | Open |
| NFR-006 | Algorithm agility | Adding support for a new signing algorithm requires changes only within the signing-backend package; no modifications to the verification API or other `core/` packages. | Maintainability | Medium | Open |
| NFR-007 | Fail-closed on backend unavailability | When the configured signing backend is unavailable, signing operations fail closed rather than falling back to weaker storage; failure surfaces with an actionable message. | Security | High | Open |
| NFR-008 | Cross-platform parity | OS keychain backend works on macOS, Linux (Secret Service or kernel keyring), and Windows (Credential Manager) with identical verification and audit behavior. | Portability | High | Open |

### Constraints

| ID | Title | Constraint | Category | Priority | Status |
|----|-------|------------|----------|----------|--------|
| C-001 | Charter architectural integrity | All signed-card and trust logic lives within a focused `core/` package (working name: `core/trust/`); other `core/` packages consume it only through its public API. No `core/` package outside the backend directory may import a KMS, HSM, or OS-keychain SDK. | Technical | High | Open |
| C-002 | No plaintext private keys anywhere in configuration | Bundle configuration, lockfiles, RPC payloads, and the event log never carry plaintext private key material. Only backend references (keychain entry name, KMS key ARN, etc.) are accepted. | Security | High | Open |
| C-003 | Append-only event log immutability | All trust decisions and trust-config changes are emitted as append-only event log entries. Corrections are new entries, not edits to prior entries. | Security | High | Open |
| C-004 | A2A spec alignment | Signed AgentCards conform to the A2A v1.0 Signed Agent Cards format; we do not invent a parallel signature envelope. Where A2A's authorization scheme formalizes further, we track the spec rather than forking. | Technical | High | Open |
| C-005 | SOC 2 readiness | Verification decisions, trust-config changes, rotations, and revocations produce audit evidence sufficient for SOC 2 audit scope, consistent with the project charter. | Regulatory | High | Open |
| C-006 | OSS / enterprise distribution split | HSM / cloud-KMS backends that live in commercial enterprise builds implement the same signing-backend contract as OSS software / OS-keychain backends. They never fork `core/`. | Business | High | Open |

### Key Entities

- **Trust Anchor**: an operator-configured credential that defines what the harness will trust. One of: a raw public key with metadata, an org identifier resolved through a configured discovery mechanism, or a pinned peer id with its key. Precedence and merging rules are explicit.
- **Signing Backend**: a pluggable provider of signing operations. Contracts: load key by reference, sign bytes, report supported algorithms, report availability. Implementations: software-in-memory (test only), OS keychain (default), cloud KMS (enterprise), HSM (enterprise).
- **Signed AgentCard**: an A2A AgentCard (see `acp-orchestration` data model) plus a signature envelope conforming to A2A v1.0.
- **Verification Result**: a typed outcome — `accepted` (with anchor id, algorithm, cache-hit/miss) or `rejected` (with a rejection-reason code from the taxonomy). Every verification produces exactly one Verification Result, and every Verification Result produces exactly one event log entry.
- **Revocation Record**: a signed assertion that a specific key id or identity is no longer trusted as of a specific timestamp. v1 distribution is explicit (see FR-007); v2 may adopt a standard mechanism as A2A publishes one.
- **Trust Event**: an append-only event log entry emitted by `core/trust/`. Kinds: verification-accepted, verification-rejected, anchor-installed, anchor-removed, key-rotated, revocation-ingested, backend-unavailable.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An operator can configure an org-scoped trust anchor and have two harness instances successfully call each other's Skills within 15 minutes of a clean clone, given valid keys.
- **SC-002**: 100 % of tampered signed AgentCards across the test matrix are rejected before any Skill invocation.
- **SC-003**: Key rotation with a 24-hour overlap window completes across the test peer set with zero failed calls attributable to rotation.
- **SC-004**: Once a revocation is published, all connected harness instances reject the revoked key within 5 minutes p95.
- **SC-005**: Zero plaintext private signing keys are found on disk, in the event log, in process arguments, or in bundle source by the automated security audit suite across the full platform matrix.
- **SC-006**: 100 % of verification decisions and trust-config changes produce append-only event log entries with sufficient detail to reproduce the decision offline.
- **SC-007**: The signing-backend contract is stable enough that a new backend (HSM, cloud KMS, or alternative OS keychain) can be added with no changes to any `core/` package outside the backend's own directory.
- **SC-008**: Verification overhead on a hot peer path does not regress A2A call performance by more than a small, measurable budget (target: under 5 ms p95).

## Assumptions

- The A2A core mission (`acp-orchestration-01KQ17ZK`) has landed before this mission ships, so signed-card envelopes have a place to attach.
- A2A v1 Signed Agent Cards use a well-defined envelope format and algorithm negotiation; this mission conforms to it rather than inventing a parallel one. If A2A subsequently publishes a formal authorization scheme, it is tracked in a follow-up rather than forked here.
- The charter's append-only event log is available as a shared surface. All trust events land in the same log as LLM, MCP, scheduler, and A2A events.
- "HSM" and "cloud KMS" backends are opt-in and ship first in the commercial enterprise build; they remain implementable through the same public signing-backend contract used by the OSS software / OS-keychain backends.
- This mission ships *only* the trust primitive. The content layer — org / team / personal context packs with signed provenance — is the `shared-context-distribution-01KQ18PA` mission.

## Open Questions

Three working defaults I will follow unless you push back; each materially shapes the implementation contract.

1. **[NEEDS CLARIFICATION]** v1 revocation distribution — for the initial release, is it acceptable to ship *manual* revocation (the operator publishes a revocation record; connected instances pull it from a configured URL or mirror on a polling schedule), with a later follow-up for automatic / push-based distribution? Default if unresolved: manual pull-based distribution with a 60-second default poll interval, operator-configurable; full CRL/OCSP-style automation deferred.
2. **[NEEDS CLARIFICATION]** Default algorithm policy — Ed25519-only by default (simple, strong, small), with ECDSA-P256 and RSA-PSS as operator opt-ins for environments where hardware or regulatory constraints require them? Default if unresolved: yes, Ed25519-only default with opt-ins.
3. **[NEEDS CLARIFICATION]** Enterprise-vs-OSS backend split — do HSM and cloud KMS backends ship in the OSS build (behind build tags, unused by default) or only in the commercial enterprise build? Default if unresolved: ship them in OSS too, behind build tags, so the split is purely licensing / support rather than a feature fork; avoids tempting an OSS user to fork `core/trust/` for KMS support.
