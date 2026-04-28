# Feature Specification: Bundle Format and Resolver

**Feature Branch**: `feat/bundle-format-resolver-01KQ1A3J`
**Created**: 2026-04-25
**Status**: Draft
**Input**: Foundation mission. Bundles are the durable, distributable artifact described in the project charter — packaging skills, MCP servers, agent definitions, hooks, context files, and now provider profiles, into a single versioned unit. This mission defines the on-disk format, the dependency-graph resolver, the lockfile, the integrity model, and the resolution lifecycle. Every other mission depends on this one.

## Why this mission exists

The charter ships the harness as "configuration is the durable artifact, the model is a swappable input." Multiple drafted specs (`llm-connector`, `acp-orchestration`, `a2a-signed-cards-trust`, `shared-context-distribution`) all assume bundles, lockfiles, and a resolver exist as a shared substrate. The existing `core/bundle/` package is a stub. Until the bundle layer has a real shape, every downstream spec is forced to either invent or assume one.

## Dependencies and relationships

- **Blocks**: every other mission. The LLM connector declares provider profiles in bundles. A2A peers, signed cards, context packs, MCP servers, agent definitions, and workflows all live in or alongside bundles.
- **Reuses**: charter doctrine on `local-first`, `security-first`, `lockfile-pinned`, `model-agnostic`, and `architectural-integrity`.
- **Adjacent**: `storage-foundations` (where resolved bundle state is persisted), `secrets-keychain` (referenced by bundle credential refs), `a2a-signed-cards-trust` (signs bundle artifacts).
- **Does not cover**: bundle UI, marketplaces, telemetry, or end-user pack authoring tooling beyond the format itself.

## User Scenarios & Testing *(mandatory)*

### User Story 1 — A bundle author packages a unit of harness configuration once and distributes it (Priority: P1)

A bundle author produces a directory tree containing a manifest, named artifacts (provider profiles, agent definitions, MCP server descriptors, context packs, hooks, scripts), and metadata (name, version, license, signatures, dependencies). They publish it to a distribution channel (git repo, OCI registry, HTTP mirror, local path). Any harness can install it by referencing it in its top-level configuration; the resolver fetches, verifies, locks, and activates it.

**Why this priority**: Bundles are the harness's reason to exist. Without a single, declarative, distributable unit, every feature ships as a one-off integration instead of composable infrastructure.

**Independent Test**: A minimal bundle is authored locally, published to a git repo, referenced by a separate harness's top-level config, and successfully resolved into a working state where its declared artifacts are visible and usable.

**Acceptance Scenarios**:

1. **Given** a valid bundle with manifest, **When** the operator references it in top-level config, **Then** the resolver fetches, verifies, and activates it.
2. **Given** a malformed manifest, **When** resolution runs, **Then** resolution fails with a structured error identifying the file and the violation, and no partial state is committed.
3. **Given** a bundle published to a distribution channel, **When** another harness installs it, **Then** the installed bundle is byte-identical to the published one (verified by content hash).

---

### User Story 2 — A team bundle and a personal overlay compose deterministically (Priority: P1)

A team's published bundle pins the team's conventions, providers, MCP servers, and skills. A personal bundle layers atop with overrides and additions. The combined dependency graph resolves deterministically: same inputs produce the same locked graph every time. The lockfile pins every transitive dependency by content hash.

**Why this priority**: The "team baseline + personal overlay" composition is the harness's central UX promise. Determinism is the charter's reproducibility / replay invariant. Without it, "what context did the model see" cannot be answered.

**Independent Test**: Two operators clone the same lockfile and run resolve. Their resolved graphs are byte-identical (same versions, same hashes, same activation order).

**Acceptance Scenarios**:

1. **Given** identical top-level configs and identical lockfiles on two machines, **When** resolution runs, **Then** the activated artifact set is byte-identical and the resolver produces the same graph order.
2. **Given** a personal bundle overlays a team bundle with conflicting names, **When** resolution runs, **Then** the personal layer wins per declared precedence and the conflict is recorded in the resolution event.
3. **Given** an attacker publishes a new bundle version with the same name as a pinned bundle, **When** resolution runs against the existing lockfile, **Then** the pinned content hash is used and the new version is ignored.

---

### User Story 3 — Bundle integrity is cryptographically verifiable (Priority: P1)

Every artifact in a bundle has a content hash. The bundle as a whole has a manifest hash that covers all artifact hashes plus metadata. The lockfile pins both. A bundle may additionally be signed by a trust anchor (managed through `a2a-signed-cards-trust`); the resolver verifies signatures when present and required. Any mismatch between declared hash and actual content fails resolution closed.

**Why this priority**: Without integrity verification, the lockfile-pinning promise is theater. Supply-chain attacks against AI-tooling bundles are an emerging threat — the harness must be locked-down by default.

**Independent Test**: A bundle is fetched, its content hash is altered by one byte, the lockfile still claims the original hash, the resolver fails closed with a typed integrity error and no artifact is activated.

**Acceptance Scenarios**:

1. **Given** a bundle with valid hashes, **When** fetched and resolved, **Then** activation succeeds.
2. **Given** a bundle whose content was modified after lockfile pinning, **When** resolved, **Then** activation fails with an integrity error identifying the artifact and both hashes.
3. **Given** an operator policy requires bundle signatures, **When** an unsigned bundle is referenced, **Then** resolution fails before any artifact is fetched and the operator is told why.

---

### User Story 4 — Resolver is fast enough for interactive use (Priority: P2)

The resolver is invoked at harness startup, on lockfile change, and on operator-initiated update. With a warm cache, end-to-end resolution of a typical bundle set finishes within the charter performance budget. Cold resolution (first install) is dominated by network fetch time but reports per-artifact progress.

**Why this priority**: The resolver runs every startup. If it is slow it becomes a daily friction tax. Charter targets bundle resolution under 500ms for typical local sets.

**Independent Test**: Resolution of a representative bundle graph (3 bundles, 15 artifacts) from a warm cache completes within budget on a developer laptop.

**Acceptance Scenarios**:

1. **Given** a warm cache, **When** the resolver runs, **Then** it completes in under 500 ms p95.
2. **Given** a cold cache, **When** the resolver runs, **Then** progress is reported per artifact and the operator can cancel mid-resolution without leaving the harness in an inconsistent state.

---

### User Story 5 — A new artifact kind is added without changing core (Priority: P3)

The bundle format ships with a known set of artifact kinds (provider profile, agent definition, MCP server descriptor, hook, context pack, etc.) and a mechanism for registering additional kinds. New kinds are added by registering a kind handler — a contract for parsing, validating, and activating that kind — without modifying any other `core/` package. Enterprise-only kinds register the same way.

**Why this priority**: Charter DIRECTIVE_001 (architectural integrity) requires extension without core surgery. P3 because v1 can ship with a fixed set; this is the on-ramp to growth without forks.

**Independent Test**: A test artifact kind is added in its own package and registered. A bundle declaring that kind is resolved successfully — without any commit to core packages outside the new handler.

**Acceptance Scenarios**:

1. **Given** a new artifact kind is registered, **When** a bundle declares it, **Then** the resolver dispatches to the new handler.
2. **Given** an attempted change that requires modifying a shared bundle interface, **When** reviewed, **Then** the architectural-integrity check flags it.

---

### Edge Cases

- A bundle declares two artifacts with the same name and kind: rejected at validation.
- A bundle's declared dependency points at an unreachable distribution channel: surface a typed "channel unreachable" error and continue serving cached state.
- A circular dependency between bundles: detected during graph construction and rejected with the cycle path identified.
- A bundle's manifest declares schema_version newer than this harness supports: rejected with a structured "schema version unsupported" error suggesting an upgrade.
- A lockfile references a content hash that no longer exists at any configured channel: resolution fails with an actionable "pinned artifact unavailable" message; resolver does not silently substitute a different version.
- A bundle attempts to declare an artifact path outside its own directory tree: rejected as a path-traversal violation.

## Requirements *(mandatory)*

### Functional Requirements

| ID | Title | User Story | Priority | Status |
|----|-------|------------|----------|--------|
| FR-001 | Bundle manifest schema | As an author, I want a versioned, declarative manifest format (YAML/TOML) covering name, version, license, dependencies, signatures, and artifact inventory. | High | Open |
| FR-002 | Artifact-kind registry | As a contributor, I want a stable artifact-kind registry that the resolver dispatches to, with a clear contract per kind. | High | Open |
| FR-003 | Distribution-channel abstraction | As an operator, I want bundles fetched from `git`, `oci`, `http_mirror`, and `local_path` channels through a stable channel contract. | High | Open |
| FR-004 | Lockfile generation and consumption | As an operator, I want a lockfile that pins every bundle and every artifact by content hash, version, and source channel. | High | Open |
| FR-005 | Deterministic resolution | As an operator, I want resolution to be deterministic given identical inputs (top-level config + lockfile + cached state). | High | Open |
| FR-006 | Content-hash integrity verification | As an operator, I want the resolver to verify content hashes for every fetched artifact and refuse to activate any artifact whose hash does not match the lockfile. | High | Open |
| FR-007 | Optional signature verification | As an operator, I want bundles optionally signed (via `a2a-signed-cards-trust`); the resolver verifies signatures when present and an operator policy may require them. | High | Open |
| FR-008 | Layered composition (team + personal) | As an operator, I want top-level config to declare ordered bundles whose artifacts compose with explicit precedence. | High | Open |
| FR-009 | Conflict detection | As an operator, I want conflicts between artifacts in different bundles surfaced (not silently merged) and resolved by declared precedence policy. | High | Open |
| FR-010 | Resolution events | As an operator, I want every fetch, verification, override, and activation recorded in the harness append-only event log. | High | Open |
| FR-011 | Local cache management | As an operator, I want a content-addressable local cache that survives bundle removal so re-installs are fast. | High | Open |
| FR-012 | Pre-flight validation | As an operator, I want manifests, lockfiles, and channel reachability validated at startup so misconfiguration surfaces before first use. | High | Open |
| FR-013 | Schema versioning and forward-compatibility hints | As an author, I want manifest schema versions and a clear upgrade path; older harnesses produce an actionable error rather than mis-parsing. | Medium | Open |
| FR-014 | Path-traversal protection | As an operator, I want artifact paths confined to bundle roots; any escape is rejected at validation. | High | Open |
| FR-015 | Cancellation safety | As an operator, I want resolution cancellable mid-run with no partial activation; cancellation rolls back to the last-known-good state. | Medium | Open |
| FR-016 | Resolver progress reporting | As an operator, I want per-artifact progress visible during cold resolution. | Medium | Open |
| FR-017 | Bundle removal | As an operator, I want to remove a bundle and have all its activations and lockfile entries cleanly torn down. | Medium | Open |
| FR-018 | Diagnostic surface | As an operator, I want `harness bundle status`, `harness bundle why <artifact>`, and `harness bundle verify` operations to inspect resolved graph and integrity state. | Medium | Open |

### Non-Functional Requirements

| ID | Title | Requirement | Category | Priority | Status |
|----|-------|-------------|----------|----------|--------|
| NFR-001 | Warm-cache resolution latency | Typical bundle set (3 bundles, 15 artifacts) resolves in under 500 ms p95 on a developer laptop. | Performance | High | Open |
| NFR-002 | Determinism rate | Identical inputs produce byte-identical resolved graphs across machines and OS targets in 100 % of test matrix runs. | Reliability | High | Open |
| NFR-003 | Integrity-verification completeness | 100 % of activated artifacts have their content hash verified against the lockfile. | Security | High | Open |
| NFR-004 | Path-traversal containment | 0 paths escaping bundle root permitted by the resolver across the audit suite. | Security | High | Open |
| NFR-005 | Local-first operation | With a populated cache, resolution succeeds with zero outbound network traffic. | Portability | High | Open |
| NFR-006 | Resolver memory ceiling | Peak resident memory during a representative resolve stays below a budget (target 200 MB). | Performance | Medium | Open |
| NFR-007 | Channel-contract extensibility | A new distribution-channel kind is addable with no changes to packages outside its own directory. | Maintainability | Medium | Open |

### Constraints

| ID | Title | Constraint | Category | Priority | Status |
|----|-------|------------|----------|----------|--------|
| C-001 | Architectural integrity | Bundle/resolver logic lives in `core/bundle/`; other `core/` packages consume it only through its public API. Channel and kind handlers live in their own packages. | Technical | High | Open |
| C-002 | No covert network egress | Resolution emits outbound network traffic only to configured channels and only when fetching is required. | Security | High | Open |
| C-003 | Append-only event log immutability | All resolution events are append-only. Corrections are new entries, not edits. | Security | High | Open |
| C-004 | Charter local-first invariant | Steady-state harness operation must function without network access once a cache is populated. | Technical | High | Open |
| C-005 | SOC 2 readiness | Resolution events produce evidence sufficient for SOC 2 audit per the charter. | Regulatory | High | Open |

### Key Entities

- **Bundle**: a versioned, distributable artifact group with a manifest, a set of named artifacts, optional signatures, and metadata. Identified by `(name, version)` plus content hash.
- **Manifest**: bundle's declarative top-level descriptor (YAML/TOML). Carries schema version, name, version, license, dependencies, artifact inventory, signature references.
- **Artifact**: one named unit within a bundle, of a registered kind (provider profile, agent definition, MCP server descriptor, hook, context pack, …). Carries kind, name, content (or content hash), kind-specific metadata.
- **Artifact Kind**: a registered handler that knows how to parse, validate, and activate a category of artifact. Stable contract; kinds are the harness's extension surface.
- **Distribution Channel**: a source of bundles. Kinds: `git`, `oci`, `http_mirror`, `local_path`.
- **Lockfile**: an operator-committed file pinning the resolved graph: every bundle by `(name, version, content_hash, channel)` plus traversal order.
- **Resolution Snapshot**: byte-stable result of one resolver run, identified by content hash; referenced by event-log entries to make replay deterministic.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An author can publish a bundle and a separate harness can install and use it within 10 minutes of a clean clone.
- **SC-002**: 100 % of resolutions across the test matrix produce byte-identical resolved graphs given identical inputs.
- **SC-003**: 100 % of activated artifacts have their content hashes verified against the lockfile.
- **SC-004**: Warm-cache resolution of a typical bundle set completes in under 500 ms p95.
- **SC-005**: A new artifact kind is added end-to-end without modifying any `core/` package outside the new handler's own directory.
- **SC-006**: Steady-state harness operation succeeds with zero outbound network traffic once the cache is warm.

## Assumptions

- The bundle format is YAML or TOML (deferred to planning; YAML expected). Text-based, Git-friendly, human-diffable.
- The harness uses a content-addressable local cache rooted in the project data directory.
- Cryptographic primitives needed for hashing (SHA-256 minimum) and signature verification are available in Go standard library or vetted third-party crypto.
- The four day-one channel kinds are sufficient for v1; private enterprise registries are addable via the channel contract.
- Operator-supplied channel credentials use the indirect credential-reference machinery from `secrets-keychain` and `llm-connector` FR-003.

## Open Questions

1. **[NEEDS CLARIFICATION]** YAML or TOML for the manifest? Default if unresolved: YAML, because every other harness configuration surface (charter, llm-connector, shared-context) already assumes YAML.
2. **[NEEDS CLARIFICATION]** Lockfile location — root of the project (`harness.lock`) or under `.harness/lock`? Default: root, mirroring `package-lock.json` / `Cargo.lock` conventions for visibility.
3. **[NEEDS CLARIFICATION]** Bundle dependency syntax — semver ranges or exact pins only? Default: ranges allowed in `harness.yaml`, exact pins required in the lockfile (the standard pattern). This keeps authoring ergonomic while enforcing reproducibility.
