# Feature Specification: Shared Context Distribution — Org, Team, and Personal Layers

**Feature Branch**: `feat/shared-context-distribution-01KQ18PA`
**Created**: 2026-04-25
**Status**: Draft
**Input**: User description: "We want to be able to have context that is distributed by a company or team or org and individual context as well. This could take the form of baseline context like definitions or explanation of a business model or skills."

## What this mission is (and is not)

**Is**: a way for organizations, teams, and individuals to author, publish, and consume *baseline context* — glossaries, domain primers, business-model explanations, skill definitions, AGENT.md-style agent guidance — as distributable, versioned, signable artifacts that layer on top of each other and are resolved into the running context of every agent workflow.

**Is not**:
- An end-user chat memory system (that belongs to the long-term-memory / RAG mission).
- A bundle format redesign (this extends the existing bundle format with a context-pack concept).
- A sharing protocol (this uses the same bundle distribution machinery wherever possible).
- An identity system (identity comes from `a2a-signed-cards-trust-01KQ18P9`).

## Dependencies and relationships

- **Depends on**: `a2a-signed-cards-trust-01KQ18P9` for cryptographic provenance — an org pack should be verifiable as "actually published by the org it claims to be."
- **Consumes**: the existing bundle resolver, lockfile, and manifest machinery declared in the charter and the llm-connector spec. Context packs are a new *kind* of artifact that flows through the same resolver.
- **Emits into**: the harness append-only event log, so every context injection is auditable.
- **Reuses**: `llm-connector-01KQ1770` FR-003 credential-reference machinery for private-pack authentication.
- **Does not cover**: vector embeddings / retrieval-augmented generation. That belongs to the long-term-memory / RAG mission. This mission is about *authored, curated* context, not indexed or embedded content.

## User Scenarios & Testing *(mandatory)*

### User Story 1 — An org publishes a canonical context pack that every member harness consumes (Priority: P1)

An organization maintains a single, authoritative context pack: glossary of internal terms, explanation of the company's business model, domain-specific conventions, pointers to canonical internal documents. The pack has a version, is signed by the org's trust anchor, and is published to a distribution location (git repository, OCI registry, HTTP mirror). Any harness configured to follow that org picks up the pack, verifies its provenance, resolves it from the lockfile, and injects it into every agent session's context so every agent in the org starts from the same shared understanding.

**Why this priority**: This is the mission's reason to exist. Without org-level shared context, every team and every individual re-explains the same business concepts to their agents, producing inconsistent outputs across the org. One authoritative source per org is the baseline enterprise ask.

**Independent Test**: An org publishes a pack containing a glossary entry. Two separate harness installations in the same org each run a minimal agent against the same prompt and both correctly reference the glossary entry's definition; an installation outside the org does not.

**Acceptance Scenarios**:

1. **Given** an operator has configured a follow on an org, **When** the org publishes a new pack version, **Then** the operator's harness picks it up on its next resolution cycle and surfaces a "context updated to version X" entry in the event log.
2. **Given** an org pack is signed and the operator trusts the org's anchor, **When** the pack is fetched, **Then** provenance verification succeeds and the pack is activated.
3. **Given** an org pack is signed by a different key than the operator trusts, **When** fetched, **Then** the pack is rejected and the operator is notified rather than the pack being silently installed.

---

### User Story 2 — A team layers team-specific context on top of the org pack (Priority: P1)

A team within the org publishes their own context pack — internal team conventions, team-specific workflows, team-scoped skills, pointers to team-owned docs. The team pack does not duplicate the org pack; it *layers* over it. When a contradiction exists (e.g., a term defined differently), the team's definition wins within the team's harnesses. The operator sees both the org layer and the team layer in their resolved context with their provenance, never as a merged anonymous blob.

**Why this priority**: Real organizations are not monoliths. A product team, a platform team, and a sales team each have legitimate local conventions. Without layering, either everyone gets the least-common-denominator org context, or local teams abandon the shared pack entirely.

**Independent Test**: An operator configures both an org pack and a team pack. For a term defined in both, the agent surfaces the team definition; for a term defined only in the org pack, the agent surfaces the org definition. The resolved context presented to the operator identifies the source of each piece.

**Acceptance Scenarios**:

1. **Given** org pack defines term X with meaning A and team pack defines X with meaning B, **When** an agent resolves context, **Then** meaning B is what the agent sees, and the resolution event records that the team layer overrode the org layer for term X.
2. **Given** a team pack references an entry that exists only in the org pack, **When** the agent resolves context, **Then** the org entry is surfaced without the team needing to re-publish it.
3. **Given** a team pack is signed by a team anchor subordinate to the org anchor, **When** the harness verifies it, **Then** the verification chain succeeds up through the org.

---

### User Story 3 — An individual adds personal overlay and preferences (Priority: P1)

An individual operator publishes a small personal pack (ephemeral, local, or optionally synced) that layers over team and org. Personal preferences, personal-scope skills, local notes, short-term project context. The personal layer is the highest-precedence override. The operator can also scope personal context to specific workflows or agents rather than applying it everywhere.

**Why this priority**: Personal overlays are the difference between "enterprise governance is something the company imposes on me" and "enterprise governance is the common ground I build my own work on." Without a personal layer, operators either resist the shared packs or maintain out-of-band workarounds.

**Independent Test**: An operator adds a personal entry that overrides a team entry. Their agent surfaces the personal entry; if they remove the personal pack, the team entry returns automatically.

**Acceptance Scenarios**:

1. **Given** org, team, and personal packs all define term X, **When** the agent resolves context, **Then** the personal layer's definition wins.
2. **Given** a personal entry is scoped to a specific workflow, **When** a different workflow runs, **Then** the personal entry does not appear in the resolved context for that other workflow.
3. **Given** the operator has not authored any personal pack, **When** agents run, **Then** they see org and team context only and the resolution event log explicitly records the absence of a personal layer rather than treating it as an error.

---

### User Story 4 — Shared context provenance is cryptographically verifiable (Priority: P1)

Every pack layer (org, team, personal) is signed by a trust anchor managed through the `a2a-signed-cards-trust` mission. At resolution time the harness verifies every layer. A tampered or unverifiable layer is rejected, and the operator sees which layer failed. Nothing is silently accepted, and nothing is silently dropped.

**Why this priority**: "Context that says it came from the company" means nothing without cryptographic provenance. Trust in the shared-context system *is* the cryptographic trust chain. An unsigned shared-context system would be a social-engineering attack surface.

**Independent Test**: A pack is altered in transit between publication and consumption. Resolution fails with a typed provenance error; the event log records the failure with the layer's identity and the rejection reason; the agent runs against the last-known-good cached version (or, if no cache, does not run at all, per operator policy).

**Acceptance Scenarios**:

1. **Given** a pack signed by an anchor the operator trusts, **When** fetched and verified, **Then** the pack is activated and the event log records the anchor id, version, and content hash.
2. **Given** a pack altered after signing, **When** verified, **Then** verification fails with the same typed taxonomy defined by `a2a-signed-cards-trust` FR-017 and the pack is rejected.
3. **Given** an operator policy of "fail closed on verification failure," **When** verification of a currently-required layer fails, **Then** no agent using that layer proceeds until the failure is resolved.

---

### User Story 5 — Context packs are versioned, lockfile-pinned, and reproducible (Priority: P2)

Packs follow the same versioning and lockfile discipline as every other bundle artifact: semver where meaningful, content-hash pinning in the lockfile, deterministic resolution from the lockfile. An operator can freeze their harness at a specific org-pack version indefinitely, or track the moving head of the distribution channel, on a per-layer basis.

**Why this priority**: Reproducibility is the harness's core charter promise. Without pinned context, a replay from an event log cannot recover the exact context the model saw, which breaks the audit story.

**Independent Test**: A session recorded on day 1 is replayed on day 30 after three intervening org-pack updates. The replay uses the pinned pack version from day 1 and the resolved context is byte-identical to the original.

**Acceptance Scenarios**:

1. **Given** a lockfile pins pack version X, **When** resolution runs, **Then** version X is fetched even if a newer version is available.
2. **Given** the operator updates the lockfile to version Y and runs the resolver, **When** fetched, **Then** the harness records the pack migration (from X to Y) in the event log and refreshes cached state.
3. **Given** an event log entry references pack version Z, **When** the operator invokes replay, **Then** pack version Z is re-fetched (from cache or distribution) and used for the replay regardless of the current head.

---

### User Story 6 — Offline and local-first by default (Priority: P2)

Once a pack is fetched, it lives in the local harness cache under the project data directory. The harness runs and resolves context without network access. Distribution channels are *sources of updates*, not dependencies of steady-state operation. Pack updates are pulled; they are never pushed over a persistent connection.

**Why this priority**: The charter makes local-first a hard invariant. A shared-context system that needs a live connection would violate it. This is P2 because the functionality works without it (it's graceful degradation), but enforcing it keeps us honest.

**Independent Test**: With all network access disabled after initial fetch, the harness resolves context for an agent session using the cached packs; the event log records a "cache-only" resolution; the agent runs successfully.

**Acceptance Scenarios**:

1. **Given** a pack has been fetched and cached, **When** the operator disables network access, **Then** resolution still succeeds against the cache and the event log records cache-only status.
2. **Given** a scheduled resolver pass is due and the network is unavailable, **When** resolution runs, **Then** the harness logs the skip, continues serving cached packs, and retries on the next scheduler tick.

---

### User Story 7 — Scoped access: not all packs are org-public (Priority: P2)

Some packs are public to everyone in the org; others are scoped to specific teams or roles; a few may be confidential and scoped to named individuals. Access scoping happens through the trust-anchor layer (`a2a-signed-cards-trust`): a pack is signed by a key whose distribution is controlled, or distribution requires an auth-ref credential. Agents never see content they are not cleared to see, even via accidental lockfile pinning.

**Why this priority**: Enterprise context includes confidential material (pricing conventions, internal strategy language, customer names). Without scoping, orgs either cannot use the shared-context feature for their most valuable content, or they rely on external access controls that the harness is unaware of and cannot audit.

**Independent Test**: A pack signed under a role-scoped anchor is offered to an operator not in that role. The operator's harness rejects resolution with a typed "access not permitted" error; the event log records the denial; no contents are cached.

**Acceptance Scenarios**:

1. **Given** a pack's access policy requires role R, **When** an operator not in role R attempts to resolve it, **Then** resolution fails with a typed permission error and no pack contents are cached.
2. **Given** an operator in role R resolves a role-scoped pack, **When** fetched, **Then** resolution succeeds and the pack activates within the operator's harness only.
3. **Given** the operator is later removed from role R, **When** the next resolution pass runs, **Then** the pack is invalidated and expunged from the local cache; the event log records the removal.

---

### User Story 8 — Every context injection is auditable (Priority: P3)

Every time a pack layer contributes content to an agent session's resolved context, an event log entry records which pack versions contributed, which specific entries were surfaced, and which layer won in any conflict. After the fact, an operator can answer "what context did this agent actually see in this session, and why?"

**Why this priority**: This is the SOC 2 audit story for context, parallel to the LLM-call audit story. P3 because a minimal v1 could defer to coarse-grained logging; full-fidelity audit is a near-term follow-up if we have to split scope.

**Independent Test**: An auditor queries the event log for a specific session and reconstructs the resolved context byte-for-byte from the recorded pack versions and overrides.

**Acceptance Scenarios**:

1. **Given** an agent session completes, **When** the event log is queried for that session, **Then** an entry exists listing every contributing pack layer, its version, its content hash, and any override decisions.
2. **Given** an override occurred (team over org, personal over team), **When** the event log is queried, **Then** the specific overridden entries and the sources are individually identifiable.

---

### Edge Cases

- A team pack references a glossary term that the org pack does not define: the term appears in resolved context from the team layer alone; no error.
- A personal pack contradicts an org pack, and the operator's policy is "fail on conflict": resolution fails with a structured conflict report identifying both sides; operator resolves by adjusting policy or pack content.
- An org pack's distribution URL becomes unreachable: the harness continues using the cached version and logs a "source unreachable, serving cache" warning; nothing fails until the cache itself is invalidated for another reason.
- A pack update removes an entry the operator's workflows depend on: the resolver surfaces a removed-entry warning on update but does not roll back automatically; operator chooses whether to pin the previous version.
- Two pack layers are signed by anchors of equal precedence and conflict: resolution fails with a typed "precedence ambiguity" error; operator must adjust anchor configuration to establish precedence.
- A pack's content hash changes without its version changing: treated as a signing violation and rejected, because the hash-to-version relationship is part of the provenance chain.
- The personal layer exceeds a configurable size budget: the harness warns the operator; content beyond the budget is not injected into agent contexts (protects token budgets and performance targets).

## Requirements *(mandatory)*

### Functional Requirements

| ID | Title | User Story | Priority | Status |
|----|-------|------------|----------|--------|
| FR-001 | Three-tier layering | As an operator, I want org, team, and personal context layers with defined precedence (personal > team > org) and a declared merge policy. | High | Open |
| FR-002 | Named context entries | As an author, I want each pack entry (glossary term, explanation, skill description, agent guidance block) to have a stable name so that later layers can override it. | High | Open |
| FR-003 | Per-layer provenance | As an operator, I want each layer's contents cryptographically signed by a trust anchor managed through `a2a-signed-cards-trust` so provenance is auditable. | High | Open |
| FR-004 | Distribution channel abstraction | As an operator, I want to follow a pack from a configured distribution channel (git repository, OCI registry, HTTP mirror) through a stable channel contract so adding a new channel kind does not require changes elsewhere. | High | Open |
| FR-005 | Lockfile-pinned versions | As an operator, I want every active pack version pinned in the bundle lockfile, so resolution is reproducible. | High | Open |
| FR-006 | Scheduled resolver | As an operator, I want pack resolution to run on a configurable schedule (e.g., at startup, on demand, and on an interval), using the existing scheduler. | High | Open |
| FR-007 | Offline / cache-only operation | As an operator, I want the harness to serve cached pack content when the distribution channel is unreachable, with clear event-log indication. | High | Open |
| FR-008 | Conflict detection and policy | As an operator, I want conflicts between layers surfaced (not silently merged); a configurable policy chooses between override-by-precedence (default) and fail-on-conflict (strict). | High | Open |
| FR-009 | Scoped access via signed credentials | As an operator, I want packs that require role or identity scoping to be served only when the operator presents a verifying credential (reusing credential-references from `llm-connector-01KQ1770`). | High | Open |
| FR-010 | Injection into agent context | As an agent author, I want resolved pack content injected into every agent session's context through a single declarative hook, with the injection point the same regardless of which layers contributed. | High | Open |
| FR-011 | Resolution-time audit event | As an operator, I want every resolution pass to emit an append-only event log entry listing every contributing pack, its version, its content hash, and any overrides applied. | High | Open |
| FR-012 | Session-time audit event | As an operator, I want every agent session to record which resolved context snapshot it consumed, so replay reproduces it exactly. | Medium | Open |
| FR-013 | Pack authoring surface | As an author, I want a simple declarative format (YAML / Markdown with frontmatter) for writing packs so authors do not need to learn a new tool. | High | Open |
| FR-014 | Pack validation | As an author, I want a validator that enforces pack schema, naming rules, signing requirements, and size budgets before publication. | Medium | Open |
| FR-015 | Workflow-scoped entries | As an author, I want personal or team pack entries that apply only to a specific workflow or agent rather than universally. | Medium | Open |
| FR-016 | Automatic expunge on scope loss | As an operator, I want role-scoped packs automatically expunged from local cache when the operator's role changes and the pack is no longer permitted. | Medium | Open |
| FR-017 | Update surface | As an operator, I want to see which packs have updates available, see the diff, and choose to accept or defer, rather than updates silently applying. | High | Open |
| FR-018 | Replay against pinned pack versions | As an operator, I want session replay to use the pack versions recorded in the session's event log entries regardless of the current head. | High | Open |

### Non-Functional Requirements

| ID | Title | Requirement | Category | Priority | Status |
|----|-------|-------------|----------|----------|--------|
| NFR-001 | Resolution latency | Full resolution of org + team + personal layers from a warm cache under 100 ms p95 on a developer laptop. | Performance | High | Open |
| NFR-002 | Size budget per layer | A single layer's resolved content is capped at a configurable size budget (default 256 KB) to protect LLM token budgets and performance targets; exceedance surfaces a warning and trims content after a declared policy. | Performance | Medium | Open |
| NFR-003 | Verification completeness | 100 % of layers active in a session have verified provenance recorded in the event log at the moment of injection. | Security | High | Open |
| NFR-004 | Offline availability | With a valid cache, the harness resolves context successfully with zero outbound network egress. | Portability | High | Open |
| NFR-005 | Scoped access completeness | 0 % of resolved context bytes from a role-scoped pack reach an operator outside that role, verified by an automated audit suite. | Security | High | Open |
| NFR-006 | Audit completeness | 100 % of resolution passes and 100 % of agent-session injections produce append-only event log entries with enough detail to reconstruct resolved context offline. | Auditability | High | Open |
| NFR-007 | Channel contract extensibility | Adding a new distribution-channel kind (e.g., a private registry protocol) requires no changes to packages outside the new channel's own directory. | Maintainability | Medium | Open |
| NFR-008 | Update fairness | No single layer update blocks other layers' resolution for more than a bounded time budget, so a stalled org mirror does not stop team-layer updates. | Reliability | Medium | Open |

### Constraints

| ID | Title | Constraint | Category | Priority | Status |
|----|-------|------------|----------|----------|--------|
| C-001 | Charter architectural integrity | Shared-context logic lives in a focused `core/` package (working name: `core/context/`); the LLM connector, the A2A adapter, and the workflow engine consume it only through its public API. Distribution-channel SDKs never leak out of their own package. | Technical | High | Open |
| C-002 | Bundle-format compatibility | Context packs live within the existing bundle format and lockfile mechanism — they are a new *kind* of bundle artifact, not a parallel configuration surface. | Technical | High | Open |
| C-003 | Provenance reuses signed-cards primitive | All pack provenance verification is delegated to the `a2a-signed-cards-trust` verification API; this mission does not reimplement signing or trust anchors. | Technical | High | Open |
| C-004 | Credential references only | Any pack access credentials (e.g., for private distribution channels) use the indirect credential-reference machinery (`llm-connector-01KQ1770` FR-003). No inline plaintext. | Security | High | Open |
| C-005 | Append-only event log immutability | Resolution events, injection events, conflict events, and update events are append-only. | Security | High | Open |
| C-006 | SOC 2 readiness | Audit, access scoping, and provenance behaviors produce evidence sufficient for SOC 2 audit per the project charter. | Regulatory | High | Open |
| C-007 | No covert network egress | Resolution never emits outbound network traffic except to configured distribution channels, and never during steady-state agent operation (only during scheduled resolver passes or on operator-initiated updates). | Security | High | Open |

### Key Entities

- **Context Pack**: a versioned, signed artifact that contains a collection of named context entries and metadata (author, issuer, license, size, content hash). A pack occupies exactly one layer (org, team, or personal) and carries its layer designation in its metadata.
- **Context Entry**: one named unit within a pack. Kinds: glossary term, explanation (prose block, typically Markdown), skill description (structured entry listing what an agent can do), agent guidance block (AGENT.md-style, scoped to workflows or agents).
- **Layer**: one of `org`, `team`, `personal`. Precedence is personal > team > org unless a layer policy explicitly overrides it. Every Context Pack declares its layer.
- **Distribution Channel**: a source of packs. Kinds: `git` (repo URL + ref), `oci` (registry reference), `http_mirror` (HTTP(S) URL + optional auth-ref). Additional kinds are addable through the channel contract.
- **Resolution Snapshot**: the byte-stable result of resolving all configured layers at a point in time. Identified by a content hash and referenced by session event log entries so replay is reproducible.
- **Context Event**: append-only event log entry emitted by `core/context/`. Kinds: `resolution_started`, `resolution_completed`, `pack_fetched`, `pack_verified`, `pack_rejected`, `override_applied`, `injection_emitted`, `cache_served`, `scope_revoked`.
- **Conflict Report**: typed structure emitted when two layers define the same entry name; consumed by the configured conflict policy (override-by-precedence or fail-on-conflict).

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An operator can configure their harness to follow an org pack, see it applied to every agent session, and reproduce a specific session's resolved context from the event log alone.
- **SC-002**: With org + team + personal layers active, overrides produce the expected precedence 100 % of the time across the test matrix.
- **SC-003**: A pack with broken provenance (tampered, wrong signer, expired key) is rejected 100 % of the time and no bytes from it are injected into any agent session.
- **SC-004**: Resolution with a warm cache completes in under 100 ms p95 on a developer laptop, with zero outbound network traffic required for steady-state operation.
- **SC-005**: Replay of a session recorded 30 days prior uses the lockfile-pinned pack versions from that session and produces a byte-identical resolved context.
- **SC-006**: A role-scoped pack's content never appears in the resolved context of an operator outside that role, across the full audit matrix (zero bytes leaked).
- **SC-007**: Every resolution pass and every injection produces an append-only event log entry sufficient to reconstruct the resolved context offline.
- **SC-008**: Adding a new distribution-channel kind requires no modifications to `core/` packages outside the new channel's own directory.

## Assumptions

- The `a2a-signed-cards-trust-01KQ18P9` mission has landed or is landing alongside this mission; its verification API is stable enough to consume.
- Org, team, and personal layering is sufficient for v1. Additional layers (department, region, project) are not in scope — they can be modeled as labels on team packs if needed.
- The existing bundle format and lockfile mechanism can accept a new `kind: context-pack` artifact type without a format redesign. If it turns out the format *does* require a redesign, that becomes its own preceding mission and this one waits on it.
- The v1 distribution channel set is `git`, `oci`, `http_mirror`; additional channels (internal registries, enterprise artifact stores) are addable via the channel contract and land in follow-up missions.
- Session-time context injection is the primary integration point with agents. Run-time context *mutation* during a session (e.g., pack updates mid-session) is out of scope for v1 — packs resolve at session start and do not change mid-session.

## Open Questions

Three working defaults I will follow unless you push back.

1. **[NEEDS CLARIFICATION]** Pack format — v1 uses YAML for metadata and Markdown (with frontmatter per entry) for prose content, one file per entry, under a declared directory structure. Default if unresolved: accept this. Pros: Git-friendly, human-diffable, no custom tooling. Cons: many small files; metadata and content live in separate but adjacent locations.
2. **[NEEDS CLARIFICATION]** Conflict policy default — when two layers define the same entry, does the harness default to *override-by-precedence* (personal > team > org, silent) or *fail-on-conflict* (operator must explicitly accept overrides)? Default if unresolved: override-by-precedence, with a clearly-visible event log entry for every override; strict mode is an operator opt-in. Rationale: too much friction on conflict-fail for common cases.
3. **[NEEDS CLARIFICATION]** Personal pack storage and sync — does the personal layer live purely local to a harness installation (simplest, fully private, no sync), or optionally sync across an operator's own devices through a configured personal distribution channel? Default if unresolved: v1 is local-only; cross-device personal sync is a follow-up mission because it needs its own identity and encryption story.
