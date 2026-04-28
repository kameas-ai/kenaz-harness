# Feature Specification: Append-Only Event Log

**Feature Branch**: `feat/event-log-01KQ1A3M`
**Created**: 2026-04-25
**Status**: Draft
**Input**: Foundation mission. The append-only event log is the harness's audit trail and replay surface, named explicitly in the charter and referenced as a shared substrate by `llm-connector`, `acp-orchestration`, `a2a-signed-cards-trust`, `shared-context-distribution`, and `bundle-format-resolver`. This mission defines its schema, ingestion path, redaction pipeline, retention model, query API, and replay primitives.

## Why this mission exists

Every drafted spec assumes "the event log" exists as a well-defined surface. Without it pinned down, each consuming mission is forced to invent or assume one; the audit/replay/branching story unravels into several incompatible logs.

## Dependencies and relationships

- **Depends on**: `storage-foundations-01KQ1A3K` (where event log entries are persisted).
- **Reuses**: `secrets-keychain-01KQ1A3M` for the redaction salt / HMAC key.
- **Blocks**: every mission that emits or consumes events — LLM connector, A2A, scheduler, MCP, signed-cards trust events, shared-context resolution events, bundle resolution events.
- **Does not cover**: replay UI (consumer of this surface, separate mission); long-term archival to S3 / cloud (future mission).

## User Scenarios & Testing *(mandatory)*

### User Story 1 — Every component emits typed events into a single append-only log (Priority: P1)

The LLM connector, MCP client/server, A2A adapter, scheduler, signed-cards trust, shared-context resolver, and bundle resolver all emit typed events into a single, shared event log. Each entry carries a stable kind, a session reference (where applicable), a timestamp, an emitter id, and a kind-specific payload. The log is the single source of truth for "what happened in this harness."

**Why this priority**: One log = one audit story = one replay model. Multiple logs = no audit story.

**Independent Test**: Run a session that exercises LLM, MCP, scheduler, and A2A. Query the log and reconstruct the full causal sequence of events from a single ordered stream.

**Acceptance Scenarios**:

1. **Given** multiple components emit events during a session, **When** the log is queried, **Then** entries from all components appear in monotonic order with consistent shape.
2. **Given** an unknown event kind is emitted (forward-compat), **When** persisted, **Then** the entry is preserved with its raw payload and is queryable; older readers ignore it without erroring.

---

### User Story 2 — Append-only is enforced — entries are never edited or deleted in place (Priority: P1)

Once written, no entry is mutated. Corrections (e.g., expanded redaction after a vulnerability is identified) are *new* entries that reference the original by id. Truncation, retention, and archival are explicit operations recorded in the log itself.

**Why this priority**: This is the integrity guarantee that makes the log usable as audit evidence. Without it, "audit log" is just "log."

**Independent Test**: An automated test attempts every reasonable mutation path (UPDATE, DELETE, file edit on the underlying database) and confirms each is rejected at the storage / API boundary or, where impossible to prevent at the DB level, detected by integrity verification.

**Acceptance Scenarios**:

1. **Given** an entry is written, **When** any code path attempts to UPDATE or DELETE it, **Then** the operation is rejected at the public API; direct database manipulation is detected by hash-chain verification.
2. **Given** a redaction needs to be expanded for a previously written entry, **When** an operator invokes a redaction, **Then** a new "redaction-applied" entry is appended that supersedes the prior payload's visibility, and the original entry remains.

---

### User Story 3 — Sensitive content is redacted before persistence (Priority: P1)

Credentials, API keys, known credential-shaped patterns, and operator-marked-sensitive fields are redacted from event payloads before the entry is written to disk. Redaction is consistent (the same input redacts the same way) and irreversible (the redacted form does not allow recovery of the original). Redaction policy is configurable; the *defaults* are conservative.

**Why this priority**: This is the SOC 2 / data-protection invariant the charter mandates. Drafted specs explicitly require it.

**Independent Test**: An automated audit suite drives a full session matrix and scans every persisted event payload for known credential patterns. Zero matches.

**Acceptance Scenarios**:

1. **Given** a request payload contains a credential-shaped substring, **When** the entry is persisted, **Then** the substring is redacted to a deterministic placeholder (with a content-hash reference for forensic correlation) before persistence.
2. **Given** an operator marks a field as sensitive in policy, **When** events with that field are emitted, **Then** the field is redacted at persistence time without requiring emitter-side awareness.

---

### User Story 4 — A session can be replayed from the log (Priority: P1)

Given a session id and a log range, an operator can deterministically reconstruct the sequence of events that occurred — what the model saw, what tools were called, what the model returned, what the user did next. Replay is byte-stable: replaying twice produces identical reconstructions.

**Why this priority**: Replay is one of the harness's three named first-class features (replay, branching, audit). Without it, "model-agnostic harness" is just a runtime; the durable artifact value collapses.

**Independent Test**: A session is recorded, then replayed; the reconstructed sequence is byte-identical to the original session's recorded sequence.

**Acceptance Scenarios**:

1. **Given** a recorded session, **When** replay is invoked with that session id, **Then** the reconstructed event sequence matches the original byte-for-byte.
2. **Given** a session that referenced bundle / context / pack versions, **When** replayed, **Then** the replay uses the *same* pinned versions recorded in the log, not the current head.

---

### User Story 5 — A session can be branched from any point (Priority: P2)

An operator selects an event in a session's log and branches: a new session starts from that point with the same context the original had at that moment. The new branch is a peer of the original and is itself replayable.

**Why this priority**: Branching is the second of three named first-class features. P2 because the underlying append-only model + replay primitive is what makes it possible; the UX layer can land slightly later.

**Independent Test**: Branch from the middle of a recorded session. The new session inherits the prior context state; subsequent events on the branch are independent and do not pollute the original.

**Acceptance Scenarios**:

1. **Given** a session at event id E, **When** an operator branches at E, **Then** a new session begins with the same accumulated context as the original at E.
2. **Given** a branched session continues with new events, **When** the original is later replayed, **Then** the original's events are unaffected by the branch.

---

### User Story 6 — Hash-chain integrity protects against tampering (Priority: P2)

Each event entry carries a hash of its payload and a reference to the previous entry's hash, forming a per-stream hash chain. An auditor can verify the chain end-to-end and detect any tampering. The harness verifies the chain on demand and on backup/restore boundaries.

**Why this priority**: Append-only-by-API is a software guarantee; hash-chained-by-content is a cryptographic guarantee. SOC 2-ready posture wants both.

**Independent Test**: Tamper with one event entry on disk; running `harness log verify` detects the break and identifies the entry.

**Acceptance Scenarios**:

1. **Given** an unmodified log, **When** verify is run, **Then** the chain validates end-to-end.
2. **Given** any single byte of a persisted entry is altered, **When** verify is run, **Then** the corruption is detected, the affected entry is identified, and the report is included in the verification result.

---

### User Story 7 — Retention policy is explicit and auditable (Priority: P3)

Operators can configure retention policy (keep all events forever; keep N days; keep until size budget exceeded). When retention triggers, events older than the policy are *archived* (moved to cold storage) or *truncated* (removed from the active log). Either is an explicit, recorded operation. The active log never silently loses data.

**Why this priority**: Without retention, every harness installation grows unbounded. With *silent* retention, the audit story breaks. P3 because v1 can ship "keep everything" by default and add policy in a follow-up.

**Independent Test**: Configure a "keep 7 days" policy, advance time by 30 days, run retention. Events older than 7 days are archived, the archive is reachable, and a "retention applied" event records the operation.

**Acceptance Scenarios**:

1. **Given** a retention policy is configured, **When** retention runs, **Then** matching events are archived or truncated and a retention event records the operation.
2. **Given** retention is unset, **When** the log grows, **Then** no events are lost and a size warning surfaces above a configurable threshold.

---

### Edge Cases

- An emitter crashes between event creation and event write: the event is dropped (not persisted). Acceptable because the action it described did not complete; durability boundary is "after write returns."
- The redaction pipeline encounters a payload with no detectable patterns but operator policy requires explicit marking: the payload passes through unredacted; the lack of redaction is itself recorded so audit can distinguish "no patterns matched" from "redaction skipped."
- Two emitters race to write events with identical timestamps: the log uses ULIDs (or equivalent monotonic id) to enforce a stable order beyond timestamp resolution.
- A consumer asks for events from a session that has been archived: the consumer is told the events are archived and given the archive reference; they are not silently returned an empty result.
- Hash-chain verification is invoked on a freshly-restored backup whose tail entries were truncated: verification reports the truncation point cleanly without aborting.
- A redaction-policy update widens redaction patterns: existing entries are *not* automatically rewritten (append-only); operators may run a "redact retroactively" operation that emits new redaction-supersession entries.

## Requirements *(mandatory)*

### Functional Requirements

| ID | Title | User Story | Priority | Status |
|----|-------|------------|----------|--------|
| FR-001 | Single shared event log surface | As a contributor, I want one event log API consumed by every emitter so all events land in a single, ordered stream. | High | Open |
| FR-002 | Typed event kinds with stable shapes | As a consumer, I want each event kind to have a stable schema; new kinds are additive and forward-compatible. | High | Open |
| FR-003 | Append-only enforcement at API | As an operator, I want the public API to expose append and read but not update or delete. | High | Open |
| FR-004 | Hash-chain integrity | As an operator, I want a hash chain across each stream so tampering is cryptographically detectable. | High | Open |
| FR-005 | Redaction pipeline | As an operator, I want events redacted by a configurable pipeline (credential-pattern matchers + operator-marked sensitive fields) before persistence. | High | Open |
| FR-006 | Deterministic redaction outputs | As an operator, I want identical inputs to redact identically (with HMAC over a server-side salt) so forensic correlation works without revealing originals. | High | Open |
| FR-007 | Persisted in storage-foundations | As a contributor, I want events persisted in the SQLite app database via the storage-foundations layer. | High | Open |
| FR-008 | Query API | As a consumer, I want to query events by session, by kind, by time range, by emitter, and by content-search where appropriate. | High | Open |
| FR-009 | Replay primitive | As a consumer, I want a deterministic replay primitive that reproduces a session's event sequence byte-identically. | High | Open |
| FR-010 | Branch primitive | As an operator, I want to branch a session from any event id, producing a new session whose initial state matches the parent at that point. | High | Open |
| FR-011 | Verify operation | As an operator, I want `harness log verify` to walk the chain and report tampering or truncation. | High | Open |
| FR-012 | Redaction-supersession events | As an operator, I want to emit new redaction events that supersede the visibility of prior payloads without mutating them. | Medium | Open |
| FR-013 | Retention policy (keep all default) | As an operator, I want a configurable retention policy; the default is keep all. | Medium | Open |
| FR-014 | Archive operation | As an operator, I want an archive operation that moves old events to a documented archive format outside the active database. | Medium | Open |
| FR-015 | Truncation operation | As an operator, I want an explicit truncation operation; it is logged before it runs and recorded after it completes. | Medium | Open |
| FR-016 | Stable event id | As a consumer, I want each event to carry a globally unique, monotonically ordered id (ULID). | High | Open |
| FR-017 | Emitter registration | As a contributor, I want emitter ids namespaced (`llm/`, `mcp/client`, `mcp/server`, `a2a/`, `scheduler/`, `bundle/`, `trust/`, `context/`, `session/`) for filterability. | High | Open |
| FR-018 | Cancellation events | As a consumer, I want cancellation, error, and timeout events to follow a uniform shape across emitters so consumers handle them once, not per-emitter. | Medium | Open |

### Non-Functional Requirements

| ID | Title | Requirement | Category | Priority | Status |
|----|-------|-------------|----------|----------|--------|
| NFR-001 | Append latency | Single event append (after redaction) completes in under 5 ms p99 on a developer laptop, matching charter performance target. | Performance | High | Open |
| NFR-002 | Redaction overhead | Redaction pipeline adds under 1 ms p95 to a typical event payload. | Performance | High | Open |
| NFR-003 | Plaintext-credential leakage | Plaintext credentials in persisted events: zero across the audit suite. | Security | High | Open |
| NFR-004 | Replay determinism | A replayed session reproduces the original event sequence byte-identically across the test matrix. | Reliability | High | Open |
| NFR-005 | Hash-chain detection | Single-byte tampering is detected by chain verification 100 % of the time. | Security | High | Open |
| NFR-006 | Forward compatibility | Older readers preserve and pass through unknown event kinds without erroring. | Maintainability | High | Open |
| NFR-007 | Query scalability | A typical session-scoped query returns in under 50 ms p95 against a 10 M-event log. | Performance | Medium | Open |

### Constraints

| ID | Title | Constraint | Category | Priority | Status |
|----|-------|------------|----------|----------|--------|
| C-001 | Architectural integrity | Event log logic lives in `core/event/`; the public API is the single emit/read/verify surface; no other `core/` package writes to the underlying tables directly. | Technical | High | Open |
| C-002 | Append-only at storage layer | Database schema and access patterns enforce append-only at the storage boundary; mutation paths are not exposed even internally. | Security | High | Open |
| C-003 | Charter local-first | Steady-state event emit and read happen entirely against the local database. Cloud archival is opt-in and out of scope here. | Technical | High | Open |
| C-004 | No plaintext credentials in payloads | Every emitted payload passes through redaction before persistence. The redaction pipeline is non-bypassable. | Security | High | Open |
| C-005 | SOC 2 readiness | Hash-chain integrity, append-only enforcement, and redaction completeness produce evidence sufficient for SOC 2 audit. | Regulatory | High | Open |

### Key Entities

- **Event Entry**: a single typed, immutable record. Fields: `event_id` (ULID), `session_id` (nullable), `emitter_id` (namespaced), `kind`, `emitted_at`, `payload` (redacted), `payload_hash`, `prev_hash`, `redaction_summary`.
- **Event Stream**: a logical ordered sequence of entries, scoped by `session_id` for hash-chain purposes (sessions are independent streams) plus a global ordering index.
- **Redaction Pipeline**: a configurable pipeline of matchers (credential patterns, structured field marks) applied before persistence. Matchers are pure functions; the pipeline is deterministic given an HMAC salt.
- **Replay Snapshot**: the byte-stable result of replaying an event range, used for branching and audit reproduction.
- **Branch**: a child session that inherits accumulated state from a parent at a chosen event id. Itself a session with its own stream.
- **Retention Policy**: an operator-configured rule (`keep_all`, `keep_n_days`, `size_budget`) plus an archival/truncation action.
- **Verification Report**: the output of a chain verification; includes "ok," tamper points, truncation points, and the verified id range.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An operator can run a session, replay it, and reproduce the original sequence byte-identically.
- **SC-002**: An operator can branch a session at any event id and the resulting branch has the same accumulated context as the parent at that point.
- **SC-003**: 100 % of persisted events pass redaction; zero plaintext credentials reach disk across the audit matrix.
- **SC-004**: 100 % of single-byte tamper attempts are detected by chain verification.
- **SC-005**: Append latency stays under 5 ms p99 under realistic concurrent load.
- **SC-006**: Adding a new emitter and a new event kind does not require modifying any other emitter or any consumer that does not handle the new kind.

## Assumptions

- The storage-foundations mission has landed; SQLite tables, WAL mode, and concurrency semantics are available as a substrate.
- The secrets-keychain mission has landed; the HMAC salt for deterministic redaction lives in OS-secure storage.
- ULIDs are an acceptable global event id format (lexicographically sortable, monotonic, 128-bit).
- "Branch" semantics for v1 reproduce accumulated context as recorded; advanced merge / rebase semantics across branches are out of scope.
- Cloud / S3 archival is out of scope and lives in a future mission; on-disk archive format here is documented but does not commit the harness to a transport.

## Open Questions

1. **[NEEDS CLARIFICATION]** Per-session hash chains vs one global chain? Default if unresolved: per-session chains plus a global ordering index. Reasoning: per-session chains let each session be replayed/branched independently of the global log; a single global chain forces all branches to interfere with each other's verification.
2. **[NEEDS CLARIFICATION]** Default retention policy — keep all (simple, audit-friendly, grows unbounded) or keep 90 days (sensible default for most users, requires retention to land in v1)? Default: keep all in v1; retention scheduler is a follow-up.
3. **[NEEDS CLARIFICATION]** Redaction-supersession semantics — when an old payload is superseded by a new redaction event, do existing replay primitives use the original or the superseded view? Default: replay uses the *current* visibility (so post-incident expanded redactions take effect immediately); a special "raw replay" mode may exist for incident response with explicit operator flag.
