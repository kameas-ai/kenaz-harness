# Implementation Plan — Append-Only Event Log

**Mission**: `event-log-01KQ1A3M`
**Spec**: `kitty-specs/event-log-01KQ1A3M/spec.md`
**Created**: 2026-04-25
**Status**: Draft (HOW)
**Charter alignment**: local-first, security-first, append-only-immutable, SOC 2-ready, DIRECTIVE_001

---

## 1. Overview

The event log is the harness's **append-only audit and replay substrate**. It is the single shared surface every emitter writes to and every consumer reads from. Replay, branching, audit, and redaction-supersession are all built on top of the same primitive: an immutable, hash-chained, redaction-filtered ordered stream of typed events.

**This mission delivers the substrate; it does not own consumer schemas.**

In scope:
- A single `Emitter` API for write, with non-bypassable redaction.
- A `Reader`/query API by session, kind, time range, emitter, content.
- A hash-chain `Verifier` (per-session chains + global ordering index).
- A deterministic `Replayer` over a session stream.
- A `Brancher` that creates a child session whose initial state matches the parent at event id E.
- Retention scaffolding (`keep_all` default in v1; archive + truncate ops in v1.x).
- Redaction-supersession events (in v1.x).
- ULID event ids; namespaced emitter ids (`llm/`, `mcp/client`, `mcp/server`, `a2a/`, `scheduler/`, `bundle/`, `trust/`, `context/`, `session/`).

Out of scope (explicit):
- Per-kind event payload schemas — each consuming mission owns its kind.
- Replay UI — consumer mission, separate spec.
- Cloud / S3 archival transport — future mission; on-disk archive format documented here only.
- Long-term archive retention scheduler — plain primitive in v1, scheduled rotation in a follow-up via the scheduler mission.

---

## 2. Architectural placement

DIRECTIVE_001 enforced: all event-log logic lives under `core/event/`. No other `core/` package writes to the events table directly; all reads happen through this package's `Reader` interface so future indexing or sharding stays internal.

```
core/event/
├── api.go              # Public types: Emitter, Reader, Verifier, Replayer, Brancher, Event, Kind, EmitterID
├── errors.go           # Typed error taxonomy (ErrChainBroken, ErrRedactionBypassed, ErrUnknownKind…)
├── log/                # Storage adapter — owns the events table + chain head cache.
│   ├── store.go        # libSQL-backed store; consumes core/storage public connection.
│   ├── migrations/     # SQL migrations registered with storage-foundations migration framework.
│   ├── append.go       # Single-writer append path; tx + chain-head update.
│   └── query.go        # Indexed reads (session, kind, emitter, time range, content).
├── redact/             # Non-bypassable redaction pipeline (C-004).
│   ├── pipeline.go     # Ordered matchers; applied unconditionally inside Emitter.
│   ├── matchers/       # Credential-pattern matchers (api keys, JWTs, AWS, Bearer, etc.).
│   ├── fields.go       # Operator-marked sensitive-field redaction by JSON path.
│   ├── hmac.go         # Deterministic placeholder generation via HMAC over keychain salt.
│   └── policy.go       # Policy load + validation; refuse "off".
├── chain/              # Hash-chain integrity primitives.
│   ├── hash.go         # Canonical payload-hash function (BLAKE3 over canonical-JSON).
│   ├── verify.go       # Walk a stream end-to-end; produce VerificationReport.
│   └── canonical.go    # Stable canonical encoding so payload_hash is reproducible.
├── replay/             # Deterministic replay primitive.
│   └── iterator.go     # Lazy session-stream iterator with byte-stable ordering.
├── branch/             # Branch primitive.
│   ├── snapshot.go     # Compute Replay Snapshot up to event id E.
│   └── fork.go         # Create child session and seed parent reference event.
├── retention/          # Retention engine (policy, archive, truncate).
│   ├── policy.go       # keep_all | keep_n_days | size_budget evaluation.
│   ├── archive.go      # Move events to documented on-disk archive bundle.
│   └── truncate.go     # Drop events; record before-and-after retention events.
└── kind/               # Built-in event-kind registry (open enum; consumers register their own).
    └── registry.go     # Stable kind ids; forward-compat: unknown kinds preserved.
```

**Cross-mission boundaries:**
- Consumes `core/storage` (storage-foundations) public connection + migration framework. Never imports SQLite/libSQL drivers directly.
- Consumes `core/secrets` (secrets-keychain) for the redaction HMAC salt — fetched as a `CredentialReference` and held as a `Secret` (`[]byte`-typed, zeroized on rotation).
- Exposed to every emitter via the `Emitter` interface only. Consumers never touch `log.Store` directly.

---

## 3. Public API

Illustrative Go signatures only — not implementation. The point is the surface, not the detail.

```go
// core/event/api.go

// EmitterID is a namespaced source identifier ("llm/anthropic", "mcp/client",
// "scheduler/", "trust/", "session/", etc.). Validated against an allowlist
// at process startup; FR-017.
type EmitterID string

// Kind is a stable, namespaced event-kind identifier owned by the producing
// mission (e.g. "llm.request.started", "mcp.tool.invoked"). Forward-compat:
// unknown kinds are preserved verbatim by older readers (NFR-006).
type Kind string

// Event is one immutable record; fields mirror spec Key Entities.
type Event struct {
    EventID           ULID            // FR-016
    SessionID         *ULID           // nullable — non-session events permitted
    EmitterID         EmitterID       // FR-017
    Kind              Kind            // FR-002
    EmittedAt         time.Time       // monotonic clock floor
    Payload           json.RawMessage // already redacted (C-004)
    PayloadHash       [32]byte        // FR-004
    PrevHash          [32]byte        // per-session chain link
    RedactionSummary  RedactionSummary
}

// Emitter is the single write surface. No update/delete (FR-003, C-002).
type Emitter interface {
    // Append validates the kind, runs the redaction pipeline, computes
    // payload_hash, links prev_hash, and persists under a single tx.
    // Returns the assigned EventID on success.
    Append(ctx context.Context, in AppendInput) (Event, error)
}

type AppendInput struct {
    SessionID *ULID
    EmitterID EmitterID
    Kind      Kind
    Payload   any // arbitrary; redacted before persistence
}

// Reader covers FR-008 query patterns.
type Reader interface {
    BySession(ctx context.Context, sid ULID, opts ReadOpts) (Cursor, error)
    ByKind(ctx context.Context, kind Kind, opts ReadOpts) (Cursor, error)
    ByEmitter(ctx context.Context, eid EmitterID, opts ReadOpts) (Cursor, error)
    ByTimeRange(ctx context.Context, from, to time.Time, opts ReadOpts) (Cursor, error)
    Search(ctx context.Context, q ContentQuery, opts ReadOpts) (Cursor, error)
    Get(ctx context.Context, id ULID) (Event, error)
}

// Verifier walks a chain end-to-end and reports tamper/truncation points.
type Verifier interface {
    VerifySession(ctx context.Context, sid ULID) (Report, error)
    VerifyAll(ctx context.Context) (Report, error) // walks every session chain
}

// Replayer reconstructs a session sequence byte-identically (FR-009, NFR-004).
type Replayer interface {
    Open(ctx context.Context, sid ULID, opts ReplayOpts) (ReplayCursor, error)
}

// Brancher creates a child session whose initial state matches the parent at E.
type Brancher interface {
    Branch(ctx context.Context, parent ULID, atEvent ULID) (ULID, error)
}

// Retention is the v1.x extension surface; v1 ships keep_all only.
type Retention interface {
    Apply(ctx context.Context, policy Policy) (RetentionReport, error)
    Archive(ctx context.Context, sel Selector, dest Archive) (ArchiveRef, error)
    Truncate(ctx context.Context, sel Selector) (TruncateReport, error)
}
```

**Boundary invariant**: `Emitter` is the only path for writes. The package exposes no `Update`, `Delete`, `Patch`, or raw `*sql.DB` — even within `core/`. `core/event/log.Store` is not exported; it's reached only via the `Emitter` and `Reader` constructors.

---

## 4. Internal layering

### 4.1 Emit path

1. `Emitter.Append` receives `AppendInput`.
2. Kind validation: kind must be either registered (built-in) or namespaced (`<emitter-namespace>.*`); unknown but well-formed kinds are accepted (FR-002 forward-compat).
3. **Redaction (non-bypassable per C-004)**: payload runs through `redact.Pipeline.Apply`. Pipeline is constructed at process start from policy + HMAC salt (`secrets-keychain`). The function call is the single ingress; no `Append` path skips it. A panic in the pipeline aborts the append (no fallback to plaintext write).
4. Canonicalize redacted payload (`chain.canonical`).
5. Compute `payload_hash = BLAKE3(canonical_payload)`.
6. Begin tx. Read `prev_hash` for the session chain head (or zero hash if first-in-session). Lock session-chain head row to serialize writes within a session.
7. Generate `event_id` as ULID (monotonic within the process; collision-resistant globally).
8. Insert row; update session-chain head; commit.
9. Return `Event`. The transaction boundary is the durability boundary (edge case: emitter crash before insert returns → event simply does not exist).

### 4.2 Read path

- Indexed queries hit composite indexes on `(session_id, event_id)`, `(kind, event_id)`, `(emitter_id, event_id)`, `(emitted_at)`. Content search routes through `Search` and currently uses SQLite FTS5 over the redacted JSON payload (FTS index built on insert).
- Cursors are forward-only and stable: a cursor opened at event id E always replays from E even if later events arrive (FR-009 byte-stability).
- `Get(id)` is a primary-key point-read.

### 4.3 Verify path

- Per-session chain walk: stream events ordered by `event_id`, recompute `payload_hash`, confirm `prev_hash` link.
- Truncation detection: gap in expected ULID order or chain-head pointer to a missing row.
- Report includes: ok span, first tamper id, last verified id, truncation point.

### 4.4 Replay path

- Lazy iterator over `Reader.BySession` with `replay-mode` filtering for redaction-supersession (default = current visibility; spec Open Question 3).
- Byte-stable: ordering by `event_id`, payload yielded as the canonicalized form recorded at append time.

### 4.5 Branch path

1. Resolve parent session + event id E.
2. Compute Replay Snapshot up to E (`branch.snapshot`).
3. Create child session id (ULID).
4. Append a `session.branched` event to the child's chain referencing `(parent_session_id, parent_event_id)`. This is the only seed event; subsequent events in the child chain proceed normally.
5. Original session is read-only with respect to the branch — writes to the branch never touch the parent's chain (acceptance scenario for US 5).

### 4.6 Retention path

- Policy evaluated against current store. Selected events to be archived/truncated are identified.
- **Before** the operation runs, an `event-log.retention.started` event is appended (FR-014/FR-015). This is itself part of the chain.
- Operation runs (archive: write to documented archive bundle outside active DB; truncate: remove rows + drop chain segment with explicit "truncation point" marker).
- **After** the operation completes, an `event-log.retention.completed` event is appended.
- Verification on a post-retention chain reports the truncation point cleanly without aborting (US 6 acceptance scenario + edge case).

---

## 5. Data model

### 5.1 `events` table (owned by this mission)

| Column | Type | Notes |
|---|---|---|
| `event_id` | TEXT (ULID) | Primary key. Lexicographically sortable, monotonic, 128-bit. |
| `session_id` | TEXT (ULID) NULL | Foreign key to `sessions` once that mission lands; nullable for harness-level events. |
| `emitter_id` | TEXT NOT NULL | Namespaced (`llm/anthropic`, `mcp/client`, …). |
| `kind` | TEXT NOT NULL | Stable, namespaced. |
| `emitted_at` | INTEGER NOT NULL | Unix nanoseconds (monotonic floor). |
| `payload` | BLOB NOT NULL | Redacted, canonicalized JSON. |
| `payload_hash` | BLOB(32) NOT NULL | BLAKE3 over canonical payload. |
| `prev_hash` | BLOB(32) NOT NULL | Previous event's `payload_hash` within the same session (zero hash for first). |
| `redaction_summary` | TEXT NOT NULL | JSON: matchers fired, field paths redacted, "no-op" if empty. |

**Indexes**:
- `PK(event_id)` (already lexicographic-sortable → global ordering index per spec Open Question 1 default).
- `(session_id, event_id)` — session-scoped chain walks and replays.
- `(kind, event_id)`.
- `(emitter_id, event_id)`.
- `(emitted_at)`.
- FTS5 virtual table over `payload` for content search.

### 5.2 `event_chain_heads` table

Internal cache to avoid re-scanning a session's tail on every append.

| Column | Type | Notes |
|---|---|---|
| `session_id` | TEXT PRIMARY KEY | |
| `head_event_id` | TEXT NOT NULL | Most recent event_id in this session. |
| `head_payload_hash` | BLOB(32) NOT NULL | Cached `prev_hash` value for the next append. |

### 5.3 `redaction_rules` table

Operator-managed pipeline configuration.

| Column | Type | Notes |
|---|---|---|
| `rule_id` | TEXT PRIMARY KEY | ULID. |
| `kind` | TEXT NOT NULL | `pattern` \| `field_path` \| `kind_specific`. |
| `definition` | TEXT NOT NULL | JSON. |
| `enabled_at` | INTEGER NOT NULL | |
| `disabled_at` | INTEGER NULL | Disable is an append; rules are not deleted. |

### 5.4 `retention_config` table

Single-row config (one logical config; rows version it).

| Column | Type | Notes |
|---|---|---|
| `version` | INTEGER PRIMARY KEY | |
| `policy` | TEXT NOT NULL | JSON: `{kind: "keep_all" \| "keep_n_days" \| "size_budget", ...}`. |
| `effective_at` | INTEGER NOT NULL | |

### 5.5 Hash chain shape — **per-session + global ordering index** (default for spec Open Question 1)

- Each session is its own chain. `prev_hash` references the prior event in the *same* session.
- The lexicographic ULID `event_id` provides a global ordering index across sessions for cross-session queries and audit.
- Trade-off accepted: branches do not share a chain with their parent. The `session.branched` event provides the cryptographic link from child → parent so audit can still walk the genealogy.

### 5.6 Emitter id namespacing (FR-017)

Allowlisted prefixes at process startup:
`llm/`, `mcp/client`, `mcp/server`, `a2a/`, `scheduler/`, `bundle/`, `trust/`, `context/`, `session/`, `event-log/` (self-events: retention, redaction-supersession), `storage/` (DB lifecycle events from storage-foundations).

Unrecognized prefix at append time → typed error; never silently accepted.

---

## 6. Integration points

### 6.1 storage-foundations (`core/storage`)

- This mission registers four migrations with the storage-foundations migration framework:
  1. `events` table + indexes + FTS5 virtual table.
  2. `event_chain_heads` table.
  3. `redaction_rules` table.
  4. `retention_config` table.
- Connection access: every read and write goes through `core/storage`'s exported connection accessor (no direct libSQL imports in `core/event/`).
- WAL mode is provided by storage-foundations; this mission does not configure pragmas.
- Append latency budget (NFR-001 < 5 ms p99) lines up with storage-foundations NFR-001.

### 6.2 secrets-keychain (`core/secrets`)

- The redaction HMAC salt is a `CredentialReference` (`{ keychain: "event-log-redaction-salt" }`).
- Resolved at process start into a `Secret` (`[]byte`-typed, never `string`); held in the redaction pipeline for the process lifetime.
- Salt rotation: on rotation, the redaction pipeline transitions to the new salt — old events keep their existing redacted forms (immutability), forensic correlation works post-rotation only across same-salt windows. This is documented in the redaction-supersession story.
- The redaction pipeline never sees plaintext credentials in the *resolved-credential* sense — it scans payloads for credential-shaped substrings and redacts them deterministically.

### 6.3 Every emitter (downstream consumers)

- Every consuming mission imports `core/event` and calls `Emitter.Append`.
- Each consumer registers its kind names under its namespace prefix; kind registry is open (no per-kind code change required in `core/event/`).
- Consumers receive cancellation/error/timeout semantics through a uniform shape (FR-018) — a small set of helper constructors in the public API (`event.Cancellation(...)`, `event.Error(...)`, `event.Timeout(...)`).

### 6.4 Frontend / RPC

- The Wails RPC surface exposes `Reader`, `Verifier`, `Replayer`, `Brancher` operations — *not* `Emitter`. Frontend cannot inject events. (Charter local-first + no leaking Wails types into `core/`: the RPC layer is in `internal/rpc/`, not `core/event/`.)

---

## 7. Phasing

### v1.0 — Substrate (this mission)

Delivers the substrate every other mission depends on:
- Single shared `Emitter` API + `Reader` API.
- Per-session hash chains + global ordering index (FR-004, NFR-005).
- Redaction pipeline with built-in credential-pattern matchers + operator field-marker matchers; HMAC-deterministic placeholders (FR-005, FR-006, NFR-003).
- Replay primitive (FR-009, NFR-004).
- Branch primitive (FR-010).
- ULID event ids (FR-016) + emitter id namespacing (FR-017).
- Query API by session, kind, time range, emitter, content (FR-008).
- `harness log verify` operation (FR-011).
- Retention default = `keep_all`. Policy field exists in config; only `keep_all` honored in v1 (FR-013 minimum).
- Storage-foundations migrations registered.
- Audit-suite of integration tests (real on-disk log per charter testing standards).

**v1.0 acceptance**: SC-001 through SC-006, NFR-001 through NFR-006 met. NFR-007 (10M-event query budget) measured but not gated; addressed pre-1.0 if it regresses.

### v1.x — Lifecycle ops

- Retention scheduler (drives `keep_n_days`, `size_budget`) — coordinates with the scheduler mission.
- Archive operation (FR-014) producing a documented on-disk archive bundle (JSON Lines + chain manifest); archive references queryable via `Reader`.
- Truncation operation (FR-015) with "logged before, recorded after" envelope.
- Redaction-supersession events (FR-012): operator-driven retroactive redaction emits new events that supersede prior visibility; default replay uses current visibility (Open Question 3 default), `--raw` flag gives original visibility for incident response only.

### v2 — Cloud archival (future mission, separate spec)

- Cloud archive transport via libSQL embedded replicas (storage-foundations roadmap item) — not in this mission.
- Cross-device replay surface — not in this mission.

---

## 8. Risk register

| ID | Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|---|
| R1 | Redaction false-negative — a credential pattern slips through and lands in the persisted payload (NFR-003 violation). | Medium | High (security incident) | (a) Layered matchers (regex library + structural field marks). (b) Audit-suite scans every persisted payload across full session matrix; CI gate. (c) Redaction-supersession (v1.x) lets us patch retroactively without mutating history. |
| R2 | Hash-chain rebuild after a *legitimate* migration or correction breaks verification end-to-end. | Medium | High (audit story collapses) | Append-only is absolute. Corrections are new events with `supersedes` references. Migrations that reshape `events` rows must hash the new shape and emit a `chain.rebased` event marking the rebase point; verifier handles the marker explicitly. |
| R3 | Query performance on 10M+ events misses NFR-007 (50 ms p95). | Medium | Medium | (a) ULID-prefix range scans on `(session_id, event_id)` cover the dominant query (session-scoped). (b) FTS5 for content search. (c) Benchmarks in CI against synthetic 10M corpus pre-1.0; if it regresses, add session-scoped partial indexes or move FTS into a sidecar table. |
| R4 | Multi-emitter concurrency on the per-session chain head causes contention and pushes append p99 above 5 ms (NFR-001). | Medium | Medium | (a) Per-session chain head locking — different sessions never block each other. (b) WAL mode (storage-foundations). (c) Benchmark concurrent emitters under realistic load; spec gate is p99 < 5 ms. (d) If contention dominates, batch a small write window per session inside `Emitter`. |
| R5 | Branch state reconstruction cost — replaying a long parent session up to E is O(N) per branch op. | Low | Medium | (a) Branches are interactive operations, not hot paths; O(N) over a single session is acceptable up to large N. (b) Replay snapshot is computed once and cached for the new session's seed event. (c) Eventual checkpoint snapshots in v2 if latency becomes a UX problem. |
| R6 | Salt rotation breaks deterministic-redaction forensic correlation across the rotation boundary. | Low | Medium | Documented: correlation windows are per-salt. Rotation emits a `redaction.salt-rotated` event with the rotation timestamp; tooling can scope correlation queries to a salt window. |
| R7 | Forward-compat unknown-kind drift — a new kind ships from a newer emitter, older readers preserve but cannot interpret it; consumers may misclassify. | Medium | Low | Kind names are namespaced; older readers see them as opaque payloads (NFR-006). UI / replay marks them "unknown kind" rather than failing. |
| R8 | Truncation removes a session referenced by an *unarchived* branch. | Low | Medium | Branch references are first-class — retention selectors must traverse `parent_session_id` references and refuse to truncate sessions that are ancestors of a non-archived branch. Pre-flight in `retention.Apply`. |
| R9 | FTS5 index size on payloads bloats the database. | Medium | Low | FTS5 over redacted payloads only; redaction shrinks long credential strings to short placeholders. Operators can disable content-search index on storage-constrained installs. |
| R10 | Operator deletes the database file out from under the harness, expecting a fresh log; chain-verify on the new file is technically valid but the audit story is gone. | Low | Medium | Storage-foundations FR-011 (single-writer enforcement) + a startup integrity check that records a `database.opened` event with prior-tail hash for cross-restart continuity audit. |

---

## 9. Open questions (planning-phase resolutions)

The spec carries three NEEDS CLARIFICATION items. Recommended defaults:

| # | Spec question | Recommended default | Rationale |
|---|---|---|---|
| OQ-1 | Per-session hash chains vs one global chain? | **Per-session chains + global ordering index via ULID `event_id`.** | Per-session chains let each session replay/branch independently. The ULID PK gives a globally sortable index for cross-session audit queries without forcing all sessions to share a single chain. Matches spec's stated default. |
| OQ-2 | Default retention policy in v1.0? | **`keep_all` in v1.0.** Retention scheduler lands in v1.x via the scheduler mission. | Audit-friendly, simple, won't lose data while the harness is still finding its shape. Size warnings surface above a configurable threshold (FR-013 acceptance scenario). |
| OQ-3 | Replay semantics under redaction-supersession? | **Default replay uses current visibility.** A `--raw` flag enables original visibility for explicit incident-response use only, gated on operator authorization and itself logged. | Honors "post-incident expanded redactions take effect immediately"; the raw mode exists as a documented exception, not a default behavior. |

Additional planning-phase decisions to surface to the user:

- **Hash function**: BLAKE3 (over SHA-256) for payload hashing — faster, modern, library-supported in Go. Confirm acceptable for SOC 2-evidence purposes (SHA-256 is the default expectation; we can swap if auditors push back).
- **Canonical encoding**: stable sort of object keys + UTF-8 bytes. Confirm there's no requirement to use RFC 8785 JSON Canonicalization Scheme specifically.
- **Archive format** (v1.x): JSON Lines with a chain-manifest sidecar. Confirm before implementing.
- **FTS5 vs JSON1 path queries** for `Reader.Search`: FTS5 is broader; JSON1 is more precise. Plan to ship FTS5 in v1.0; expose JSON-path queries if/when consumers need them.

---

## 10. Test strategy alignment (charter)

- Black-box integration tests for **append-only event log invariants** are charter-mandated (testing standards). Real on-disk log under tempdir; no mocking the event log in tests that assert audit/replay behavior.
- Property-based tests for the redaction pipeline: random credential-shaped inputs through random session shapes; assert zero plaintext on disk.
- Property-based tests for chain verification: tamper a random byte at a random offset; assert detection.
- Replay determinism: record-replay-rerecord cycle with byte-equality assertion across the test matrix.
- Concurrency: ten emitters writing to ten sessions for one minute under `go test -race`; assert zero deadlocks, zero lost writes, zero chain breaks.
- Performance gates: bench harness covering NFR-001 (5 ms p99 append), NFR-002 (1 ms p95 redaction), NFR-007 (50 ms p95 query against 10M synthetic corpus). Run on CI hardware with documented baselines.

DIRECTIVE_036 (black-box only): integration tests drive the public `Emitter`/`Reader`/`Verifier`/`Replayer`/`Brancher` API; tests do not import internal packages or assert on `events` table layout. Schema tests live in storage-foundations.

---

## 11. ADR commitments

Per DIRECTIVE_003, the following decisions warrant ADRs alongside this plan:

- **ADR**: Per-session hash chains + ULID global ordering (resolves OQ-1).
- **ADR**: BLAKE3 + canonical-JSON for payload_hash (planning-phase decision above).
- **ADR**: Redaction pipeline non-bypassable design (C-004 enforcement strategy).
- **ADR**: Default replay uses current visibility under redaction-supersession (resolves OQ-3).

---

## 12. Acceptance traceability

| Spec ID | Plan section | Notes |
|---|---|---|
| FR-001 | §2, §3 | Single `Emitter` interface; one shared package. |
| FR-002 | §3, §5.6, §10 | Open kind registry; namespaced; forward-compat. |
| FR-003 | §3, §4.1 | No update/delete on the public API. |
| FR-004 | §4.1, §4.3, §5 | BLAKE3 chain over canonical payload. |
| FR-005, FR-006 | §2 (`redact/`), §4.1, §6.2 | Pipeline with HMAC over keychain salt. |
| FR-007 | §6.1 | Migrations registered; libSQL via `core/storage`. |
| FR-008 | §3, §4.2 | Indexed query API; FTS5 for content search. |
| FR-009 | §3, §4.4 | Lazy iterator; byte-stable. |
| FR-010 | §3, §4.5 | Snapshot + new-session seed event. |
| FR-011 | §3, §4.3 | `harness log verify` operation. |
| FR-012 | §7 (v1.x) | Redaction-supersession events. |
| FR-013 | §7 (v1.0 keep_all; v1.x scheduler) | Default policy honored in v1.0. |
| FR-014 | §7 (v1.x), §4.6 | Archive operation. |
| FR-015 | §7 (v1.x), §4.6 | Truncation operation; logged before/after. |
| FR-016 | §3, §5 | ULID event ids. |
| FR-017 | §5.6 | Emitter id namespacing. |
| FR-018 | §6.3 | Helper constructors for cancel/error/timeout. |
| NFR-001 | §10, R4 | 5 ms p99 append; benchmark gate. |
| NFR-002 | §10 | 1 ms p95 redaction; benchmark gate. |
| NFR-003 | §10, R1 | Audit-suite gate; zero plaintext. |
| NFR-004 | §10 | Replay determinism; record-replay-rerecord. |
| NFR-005 | §10 | Hash-chain detection at 100 %; property test. |
| NFR-006 | §5.6, §10 | Forward-compat unknown kinds. |
| NFR-007 | §10, R3 | 10M-event query benchmark. |
| C-001 | §2 | All event logic under `core/event/`. |
| C-002 | §3, §4.1 | Append-only at storage layer (no mutation paths exposed). |
| C-003 | §6 | Local-first; no network egress steady-state. |
| C-004 | §2, §3, §4.1 | Redaction non-bypassable; Emitter is the only ingress. |
| C-005 | §10, §11 | Audit evidence via verify op, ADRs, append-only invariant. |

---

*End of plan. Total length within 800-line ceiling.*
