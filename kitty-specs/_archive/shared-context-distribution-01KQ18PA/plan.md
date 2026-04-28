# Implementation Plan: Shared Context Distribution

**Mission**: `shared-context-distribution-01KQ18PA`
**Spec**: `kitty-specs/shared-context-distribution-01KQ18PA/spec.md`
**Charter**: `.kittify/charter/charter.md` (DIRECTIVE_001 architectural integrity, append-only event log, local-first, SOC 2-ready)
**Status**: Draft (HOW only — no implementation in this mission)

This plan describes *how* to realise the spec's 18 FRs, 8 NFRs, and 7 constraints. It does **not** prescribe code. Public Go interface signatures appear only as illustrative examples to fix the contract surface; field names, error taxonomies, and module wiring are subject to refinement during work-package decomposition.

---

## 1. Overview

Shared-context distribution is the **content layer** that lives on top of the bundle distribution + signing substrate. It introduces a new bundle-artifact kind — `context-pack` — and the runtime machinery that turns those artifacts into the *resolved context* every agent session sees at start-of-session.

Three responsibilities define this mission's boundary:

1. **Author-side**: a declarative pack format (YAML metadata + Markdown entries with frontmatter), validated and signable, publishable through any bundle distribution channel.
2. **Resolver-side**: layered merge of org → team → personal packs into a content-addressed *Resolution Snapshot*; conflict reporting with a configurable policy; cryptographic provenance verification per layer; lockfile pinning; offline cache; role-scoped access enforcement.
3. **Runtime-side**: a single declarative injection hook that hands the snapshot to every agent session at session start, and audits both the resolution and the injection in the append-only event log.

This mission does **not** redesign the bundle format, the signing primitive, the event log, or the credential-reference machinery. It consumes their public APIs.

---

## 2. Architectural Placement

Per **C-001** and DIRECTIVE_001 the entire content layer lives in a focused package, working name **`core/context/`**. No package outside `core/context/` may know that "org / team / personal" is the layering model; they only see the consolidated `ResolutionSnapshot`.

```
core/
  bundle/        # existing, owns artifact-kind registry + channel contract + lockfile (mission KQ1A3J)
  trust/         # signing/verification API (mission KQ18P9)
  event/         # append-only event log (mission KQ1A3M)
  secret/        # credential-reference resolution (mission KQ1A3M)
  context/       # THIS MISSION
    pack/        # on-disk pack format parser + validator
    layer/       # three-tier merge engine and override registry
    resolver/    # ContextResolver — the public entry point; orchestrates fetch+verify+merge+snapshot
    access/      # role/identity scope checker, consumes credential refs
    cache/       # offline pack cache + GC + snapshot store
    inject/      # session-time injection hook + per-agent shaping
    audit/       # context-event emission, schemas, and query helpers
    replay/      # snapshot reconstruction from event log + lockfile
    policy/      # conflict policy, size-budget policy, fail-closed policy
```

**Boundaries that must not leak** (DIRECTIVE_001):

- `core/context/` consumes `core/bundle/` only via its public artifact-kind registry and channel contract. It registers a `context-pack` kind handler; it never reaches around the resolver.
- `core/context/` consumes `core/trust/` only via the verification API surface defined by mission KQ18P9 FR-012. It never imports a signing-backend SDK.
- `core/context/` consumes `core/event/` only as an emitter; it never writes to the event log's underlying store.
- `core/context/` resolves credentials only through the `core/secret/` reference machinery. No inline plaintext anywhere in pack metadata, pack content, or channel config.
- `core/llm/`, `core/session/`, `core/mcp/`, and the workflow engine consume only `ContextResolver.Resolve(...)` and the `ResolutionSnapshot` it returns.

**Distribution channels are not duplicated.** The `git`, `oci`, and `http_mirror` channel implementations live in `core/bundle/` (per KQ1A3J FR-003). `core/context/` registers a `context-pack` *artifact kind* against the existing channels; it does not ship channels of its own. NFR-007 (channel extensibility) is therefore inherited automatically — adding a new channel is a `core/bundle/` change with zero edits in `core/context/`.

---

## 3. Public API (illustrative)

The public API is the surface other `core/` packages consume. Signatures below pin contract intent; final names land in plan-phase work packages.

```go
// Package core/context exposes a single public entry point.
// All other types in this snippet are illustrative.

type ContextResolver interface {
    // Resolve produces a ResolutionSnapshot from configured layers, consulting
    // cache, channel, and lockfile. Pure-cache mode (offline) is supported when
    // the cache covers the lockfile-pinned versions.
    Resolve(ctx context.Context, req ResolveRequest) (ResolutionSnapshot, error)

    // Replay reconstructs a snapshot from a recorded event-log range without
    // contacting any distribution channel.
    Replay(ctx context.Context, sessionID string) (ResolutionSnapshot, error)

    // Inject hands a previously-resolved snapshot to a session at session-start.
    // Returns the injection event id for audit linkage.
    Inject(ctx context.Context, sessionID string, snap ResolutionSnapshot, scope SessionScope) (EventID, error)
}

type ResolveRequest struct {
    LockfilePath string
    Layers       []LayerRef          // org, team, personal in declared precedence
    Policy       ResolutionPolicy
    Workflow     string              // for FR-015 workflow scoping
    AgentID      string              // for FR-015 agent-scope filtering
}

type ResolutionSnapshot struct {
    ID            ContentHash         // identifies the snapshot byte-stably
    Layers        []LayerActivation   // ordered: org, team, personal
    Entries       []ResolvedEntry     // post-merge, post-policy, post-budget
    Conflicts     []ConflictReport    // empty iff no detected conflicts
    Overrides     []OverrideRecord    // who beat whom and why
    Provenance    []ProvenanceRecord  // anchor id + algorithm + hash per layer
    GeneratedAt   time.Time
    Mode          ResolutionMode      // "fresh", "cache-only", "stale-warn"
}

type ContextPack struct {
    Name        string
    Version     semver.Version
    Layer       Layer                 // org | team | personal
    Issuer      string
    Entries     []ContextEntry
    SizeBytes   int64
    ContentHash ContentHash
}

type ContextEntry struct {
    Name        string                // stable, used for override matching (FR-002)
    Kind        EntryKind             // glossary | explanation | skill | guidance
    Body        []byte                // typically Markdown, opaque to merge engine
    Frontmatter map[string]any        // workflow/agent scope, tags
    SourceLayer Layer
    SourcePack  PackRef
    ContentHash ContentHash
}

type ConflictReport struct {
    EntryName  string
    LeftLayer  Layer
    RightLayer Layer
    Resolved   bool                   // true if a policy chose a winner
    Winner     Layer                  // valid iff Resolved
    Reason     string
}

type AccessPolicy struct {
    RequiredRoles []string
    CredentialRef secret.Ref          // resolved via core/secret
}
```

The **only** public symbol other packages should reference is `ContextResolver`. The supporting structs are returned through it; they are immutable from the consumer's perspective.

---

## 4. Internal Layering

### 4.1 Pack ingester (`core/context/pack`)

- Parses YAML 1.2 (the manifest format chosen for bundles in mission KQ1A3J D1) using the same parser pin as `core/bundle/`. Reuses validators from `core/bundle/` where applicable.
- Walks the pack directory tree. Each entry is one Markdown file with YAML frontmatter (the spec's working assumption — see Open Questions §9).
- Emits a typed `ContextPack` value. Path-traversal containment inherited from `core/bundle/` FR-014.
- Validates **before** any verification call: schema, naming uniqueness within a pack, size-per-layer (NFR-002 default 256 KB), required signature reference.

### 4.2 Provenance verifier

- Per **C-003** never reimplements signing. Calls `core/trust.Verifier.Verify(payload, envelope, policy)` once per pack.
- Failure surfaces use the exact taxonomy from `a2a-signed-cards-trust` FR-017 (signature invalid, algorithm not permitted, anchor missing, anchor removed, key revoked, key expired, identity collision, clock skew). No new error codes.
- Verification records include anchor id, algorithm, cache state — passed into the audit emitter unchanged for SOC 2 forensics.

### 4.3 Three-tier merge engine (`core/context/layer`)

- Resolves layers in spec-declared precedence: **personal > team > org** (FR-001).
- The override registry is **entry-name-keyed**. Two entries with identical `Name` from different layers form an override pair; the higher-precedence layer wins by default.
- Workflow / agent scoping (FR-015) is applied **before** override evaluation: an entry whose frontmatter restricts it to workflow X is filtered out for any other workflow's resolution.
- Output: a deterministic `[]ResolvedEntry` in name order plus `[]OverrideRecord` and `[]ConflictReport`.
- "Conflict" vs "override" is policy-driven (see §4.7): in `override-by-precedence` mode every override is recorded but not failed; in `fail-on-conflict` mode each name collision becomes a `ConflictReport` and resolution returns a typed error.

### 4.4 Scope-access checker (`core/context/access`)

- Consumes a pack's declared `AccessPolicy` (e.g., role gate, credential gate).
- For role gates: queries the operator's resolved role set (out of scope — that comes from operator identity, eventually from `core/trust/`). For v1, role membership is read from a configuration file under the project data directory, validated at pre-flight.
- For credential gates: resolves a `secret.Ref` from `core/secret/`. If the credential is missing or expired, the pack is **not** cached, and a `pack_rejected` event is emitted with the typed reason. Implements **FR-009**.
- Implements **FR-016**: when a previously-permitted pack's role set changes (operator removed from role R), the next resolution pass invalidates the cache entry, emits `scope_revoked`, and expunges local content.

### 4.5 Pack cache (`core/context/cache`)

- Content-addressed cache rooted under the project data directory, structured as `cache/context/sha256/<digest>/`.
- Cache reuses `core/bundle/`'s content-addressable cache where the bundle resolver already stores fetched artifacts; this layer adds context-pack-specific indexes for fast layer lookup.
- Implements **FR-007 / NFR-004**: when network is unreachable but the lockfile-pinned hash is in the cache, resolution succeeds in `cache-only` mode and records the mode in the resolution event.
- GC is conservative: cache entries are kept across pack removal so re-installs are fast (parallels `core/bundle/` FR-011).

### 4.6 Resolution snapshot store (`core/context/cache` + `replay`)

- Each `ResolutionSnapshot` is content-hashed and persisted (likely SQLite via the `storage-foundations` substrate) so that **FR-018** replay can reconstruct it cheaply.
- The session event log entry from FR-012 includes the snapshot id; replay (`ContextResolver.Replay`) re-fetches by id from the local snapshot store, falling back to re-resolving from the lockfile-pinned pack versions referenced in the event-log entries when the snapshot itself has aged out of local store.

### 4.7 Conflict / size / fail-closed policies (`core/context/policy`)

Policy is a typed struct on `ResolveRequest`. Three knobs:

| Policy | Default | Strict alternative |
|---|---|---|
| Conflict | `override-by-precedence` (silent winner, override recorded) | `fail-on-conflict` (typed error returned, no snapshot) |
| Size budget | NFR-002 default 256 KB per layer; trim policy `keep-by-name-order-then-warn` | Operator may set hard-fail-on-overflow |
| Verification failure | `fail-closed` for any *required* layer (FR-004 acceptance scenario) | Operator may declare org pack optional |

`override-by-precedence` is the v1 default for the conflict knob. See **Open Questions §9** — this default is the one the spec proposes pending operator pushback.

### 4.8 Session-time injector (`core/context/inject`)

- Single declarative hook called by `core/session/` at session start (FR-010).
- Receives the `ResolutionSnapshot` and shapes it into the in-memory representation the LLM connector / agent expects (likely a list of system-message-style blocks plus structured skill/guidance metadata).
- Emits one `injection_emitted` event per session, carrying the snapshot id, the contributing pack ids, and the agent id (NFR-006, FR-012).
- v1 is **session-start only** — packs do not change mid-session (Assumption in spec). Mid-session updates would be a follow-up hook on the same surface.

---

## 5. Data Model

### 5.1 Pack on-disk format (default — see Open Questions §9)

```
my-org-context/                         # pack root inside a bundle's tree
  pack.yaml                             # metadata: name, version, layer, issuer, signatures, access policy
  entries/
    glossary/
      entropy.md                        # frontmatter: name, kind=glossary, scope (optional)
      tco.md
    explanations/
      business-model.md
    skills/
      run-quarterly-rollup.md
    guidance/
      AGENT.md
  signatures/
    pack.sig                            # detached signature envelope per a2a-signed-cards-trust
```

The layout is illustrative; the validator enforces shape, not exact directory names. Entries are individually content-hashed; the manifest summarises with a Merkle-style aggregate hash that the trust layer signs.

### 5.2 Bundle artifact-kind registration

`core/context/` registers exactly one new kind with `core/bundle/`:

- Kind id: `context-pack`
- Handler responsibilities: parse pack manifest, validate, surface `ContextPack` value, attach to lockfile entry, mark cacheable.
- Lockfile entry shape (extending the Cargo-flavoured TOML defined in mission KQ1A3J D6):

```toml
[[artifact]]
kind         = "context-pack"
name         = "acme-org-context"
version      = "1.4.2"
layer        = "org"
content_hash = "sha256:..."
source       = "oci://registry.acme.example/contexts/org:1.4.2"
signature    = "sigstore-bundle:..."
required     = true                   # if true, verification failure is fatal
```

### 5.3 Resolution-snapshot schema (SQLite, via storage-foundations)

```
context_resolution_snapshot
  id              TEXT PRIMARY KEY     -- content hash
  generated_at    TEXT NOT NULL
  lockfile_hash   TEXT NOT NULL
  mode            TEXT NOT NULL        -- fresh | cache-only | stale-warn
  body            BLOB NOT NULL        -- canonical JSON of ResolutionSnapshot

context_resolution_pack
  snapshot_id     TEXT REFERENCES context_resolution_snapshot(id)
  layer           TEXT NOT NULL
  pack_name       TEXT NOT NULL
  pack_version    TEXT NOT NULL
  pack_hash       TEXT NOT NULL
  anchor_id       TEXT NOT NULL
```

This is store-only; the event log carries the canonical record per **C-005**.

### 5.4 Event kinds (emitted to `core/event/`)

Emitter id: `context/`. Per FR-017 in `event-log-01KQ1A3M`.

| Kind | Payload (post-redaction) | FR ref |
|---|---|---|
| `resolution_started` | request id, lockfile hash, layer count | FR-011 |
| `pack_fetched` | pack name + version + content hash + channel | FR-011 |
| `pack_verified` | pack id + anchor id + algorithm + cache state | FR-003, NFR-003 |
| `pack_rejected` | pack id + rejection reason from trust taxonomy | FR-003 |
| `override_applied` | entry name + winner layer + loser layer | FR-008 |
| `cache_served` | snapshot id + mode = `cache-only` | FR-007 |
| `resolution_completed` | snapshot id + duration + override count | FR-011 |
| `injection_emitted` | session id + snapshot id + agent id | FR-010, FR-012 |
| `scope_revoked` | pack id + role removed | FR-016 |
| `update_available` | pack id + current version + available version + diff summary | FR-017 |

All payloads pass through the redaction pipeline before persistence (per `event-log` C-004).

---

## 6. Integration Points

| Mission | Surface consumed | How |
|---|---|---|
| `bundle-format-resolver-01KQ1A3J` | Artifact-kind registry, channel contract, lockfile format, content-addressable cache | Register `context-pack` kind handler; lockfile entries written through bundle's lockfile API; channel ops dispatched through bundle resolver |
| `a2a-signed-cards-trust-01KQ18P9` | `core/trust.Verifier` | Per-pack provenance verification. Errors mapped 1:1 from FR-017 taxonomy |
| `event-log-01KQ1A3M` | Emitter API, query API, replay primitive | Emit context-events; consume query for replay; never bypass redaction |
| `secrets-keychain-01KQ1A3M` | `secret.Ref` resolution | Channel auth + access-policy credential gates resolved by reference only (C-004) |
| `storage-foundations-*` | SQLite tables for snapshot store | New tables under context schema namespace |
| `acp-orchestration-*` (future) | A2A propagation of resolved-context references | Out of scope here; the snapshot id is stable so cross-instance reference is trivial later |
| Workflow engine / `core/session/` | `ContextResolver.Inject` | Session-start hook is the *only* injection surface (FR-010 invariant) |

A simple **Component view** (C4 Level 3, per the charter's c4-incremental-detail-modeling paradigm):

```
[session]──Inject──>[ContextResolver]
                        │
        ┌───────────────┼───────────────┬───────────────┐
        ▼               ▼               ▼               ▼
   [pack ingester]  [trust.Verify]  [layer merge]   [audit emit]
        │                                                │
        ▼                                                ▼
   [bundle channel] ◄── lockfile pin ──► [pack cache]  [event log]
```

---

## 7. Phasing

### v1.0 (this mission)

- 3-tier layering with personal > team > org precedence (FR-001).
- Named entries with override registry (FR-002).
- Per-layer signed provenance via `core/trust/` (FR-003, NFR-003).
- Distribution-channel reuse for `git`, `oci`, `http_mirror` (FR-004) — no new channel kinds added here.
- Lockfile pinning for every active pack (FR-005, NFR-002 determinism).
- Scheduled resolver pass via existing `core/scheduler/` (FR-006).
- Offline / cache-only operation (FR-007, NFR-004).
- Conflict detection with `override-by-precedence` default (FR-008).
- Role-scoped access via credential refs (FR-009, NFR-005).
- Session-start declarative injection hook (FR-010).
- Resolution-time and session-time audit events (FR-011, FR-012, NFR-006).
- YAML+Markdown pack format with a validator (FR-013, FR-014).
- Workflow / agent-scoped entries (FR-015).
- Auto-expunge on scope loss (FR-016).
- Update surface — surfaces availability and a diff, requires explicit accept (FR-017).
- Replay against pinned versions (FR-018, SC-005).

### v1.x (near-term follow-ups)

- `fail-on-conflict` strict mode wired in if operator feedback shows the default is wrong.
- Diff-on-update with a pre-merge preview UI (UI surface, separate mission).
- Personal pack templates / scaffolding CLI for authors.
- `harness context why <entry>` diagnostic (parallels `harness bundle why`).

### v2 (later)

- Cross-device personal pack sync (Open Questions §9.3) — needs its own identity + encryption story.
- Additional distribution channels added through `core/bundle/`'s channel contract; no `core/context/` change required.
- Run-time pack mutation mid-session (currently out of scope per spec assumption).
- Cross-org context federation — packs that span multiple trust anchors.

---

## 8. Risk Register

| # | Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|---|
| R1 | Large org packs blow LLM token budget at injection time | Medium | High | NFR-002 size budget per layer with trim-policy; emit `update_available` warning when the new version would exceed budget; offer per-workflow scoping (FR-015) so big packs apply only where needed |
| R2 | Role-scope race: operator removed from role R after resolution but before injection | Low | High | Re-check access policy at injection time, not just resolution time; emit `scope_revoked` and abort injection if role lost |
| R3 | Cache staleness vs replay determinism — replay from event log expects specific pack versions; cache may have only newer | Medium | High | Snapshot store (§4.6) keeps the post-merge snapshot keyed by content hash so replay does not need raw packs; raw pack cache is best-effort secondary |
| R4 | Distribution-channel auth quirks (ECR 12h tokens, GAR short-lived) leak into `core/context/` | Low | Medium | Mitigated by C-001: all auth lives in `core/bundle/` channel handlers; `core/context/` only sees `secret.Ref` opaquely |
| R5 | Provenance verification failure makes a *required* org layer unresolvable, halts all sessions | Medium | High | FR-007 + cache-only mode allows last-known-good fallback; operator policy explicitly choses fail-closed vs serve-cache; both audited |
| R6 | Pack format ambiguity (Markdown frontmatter vs body) causes silent override misses | Low | Medium | Validator enforces required frontmatter fields; entry-name uniqueness within a pack is rejected at validation; integration tests cover override resolution |
| R7 | SOC 2 audit reveals event log entries don't fully reconstruct snapshot offline | Low | High | NFR-006 mandates 100 % completeness; integration tests in the audit suite reconstruct snapshots from event logs alone, byte-comparing against stored snapshots |
| R8 | A team pack signed by a key chain whose intermediate is rotated mid-resolution | Low | Medium | Trust layer's grace-period (mission KQ18P9 FR-013) handles this transparently; we surface the grace state in `pack_verified` audit payload |
| R9 | Conflicting precedence between two anchor configurations causes "precedence ambiguity" (spec edge case) | Low | Medium | Surface typed `precedence_ambiguity` error from trust layer; resolution refuses rather than guessing; operator must adjust anchor config |
| R10 | Personal pack accidentally checked into VCS exposes confidential operator notes | Low | Medium | v1 personal pack lives under `~/.kaneaz/context/personal/`, **not** under the project tree; `.gitignore` template for any in-repo personal overlay |

---

## 9. Open Questions for the User

The spec flags three NEEDS CLARIFICATION items. The plan adopts the spec's working defaults pending operator pushback, but each one materially affects work-package shape.

1. **Pack format**
   *Spec default*: YAML 1.2 metadata + Markdown entries with YAML frontmatter, one file per entry, declared directory structure.
   *Plan stance*: accept. It's git-friendly, human-diffable, and reuses the YAML 1.2 parser already pinned by `bundle-format-resolver`.
   *Push-back trigger*: operator preference for a single-file pack format (e.g., one YAML document with embedded entries) for simpler authoring tooling. Would simplify validation, complicate diff review.

2. **Default conflict policy**
   *Spec default*: `override-by-precedence` (silent winner, override recorded in event log).
   *Plan stance*: accept. The strict mode is implemented but opt-in.
   *Push-back trigger*: SOC 2 reviewer or compliance officer wants every override to be operator-acknowledged. Switching the default to `fail-on-conflict` is a config-flag change with no code change.

3. **Personal pack storage and sync**
   *Spec default*: v1 is local-only; cross-device sync is a follow-up mission.
   *Plan stance*: accept. Personal pack lives under the project data directory or operator home (per OS conventions), unsigned by default but signable with a personal trust anchor if the operator has one.
   *Push-back trigger*: operator wants personal pack to flow through the same OCI registry as team packs, scoped by personal credential. Doable through the existing channel contract; no new code in `core/context/`, just config templates and docs.

The first two have no blocking effect on plan-phase work-package decomposition — adopting the defaults lets us proceed. The third determines whether v1 ships any cross-device personal-sync wiring (currently planned **out**).

---

## 10. What this plan does *not* commit to

Recorded explicitly so reviewers can flag scope creep:

- **No** redesign of the bundle format. C-002 is a hard constraint.
- **No** new signing primitive, no new trust anchor, no new key-management story. C-003.
- **No** plaintext credentials anywhere — channel auth and access-policy gates resolve via `secret.Ref` only. C-004.
- **No** vector embedding, RAG, or retrieval logic. The spec explicitly excludes this.
- **No** mid-session pack mutation. Session-start only.
- **No** new distribution channel kinds in this mission. New channels land via `core/bundle/`.
- **No** UI surface for diffing or accepting updates. The CLI / RPC surface emits the typed update-available event; the frontend that renders it is a separate mission.

---

## 11. Acceptance signals

Mission is implementable when:

- `core/context/ContextResolver` compiles against the public APIs of `core/bundle`, `core/trust`, `core/event`, and `core/secret` with no hidden imports.
- An integration test demonstrates org-only, org+team, and org+team+personal resolution end-to-end against a local OCI registry, including signed packs, content-hash pinning in the lockfile, and replay from event log.
- An audit test demonstrates **zero** plaintext credential bytes in the event log across the full layered scenario, **zero** role-scoped pack bytes leaking to an out-of-role operator, and **100 %** snapshot reconstruction from event-log entries alone.
- Bench harness shows full resolution from warm cache under 100 ms p95 (NFR-001).
- A new channel kind (test fixture) added to `core/bundle/` produces working context-pack distribution with **zero** changes in `core/context/` (NFR-007 / SC-008).
