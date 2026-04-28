# Feature Specification: ACP Orchestration — A2A Agent-to-Agent Protocol Surface

**Feature Branch**: `feat/acp-orchestration-01KQ17ZK`
**Created**: 2026-04-25
**Status**: Draft
**Input**: User description: "Determine how the industry is starting to support 'ACP' and exactly what it means for orchestrating multiple agents and flows. Plan the harness's adoption: client and server modes, local-first deployment, audit-through-event-log, defer cross-org trust to a follow-up." Research has resolved the term: the canonical agent-to-agent orchestration protocol is **A2A v1.0** (Linux Foundation, released 2026-03-12). Anthropic's separately-named "ACP" (editor↔agent) is out of scope for this mission.

## Why this mission exists

The harness's value proposition is configuration-first orchestration of agents and workflows; that promise is hollow without a wire protocol for agent-to-agent communication that the rest of the industry actually uses. A2A v1.0 is now that protocol — Linux Foundation governed, JSON-RPC 2.0 over HTTP(S), supported by 150+ organizations, with a first-party Go SDK. Adopting A2A as both a client and a server lets the harness participate in the broader agent ecosystem and gives the future Python LangChain daemon a clean wire boundary instead of a fragile in-process FFI bridge.

This mission scopes how the harness speaks A2A: how peers are declared in bundles, how local agents and workflows are exposed as A2A endpoints, how outbound calls are dispatched, how the wire surface is wrapped so that no other `core/` package depends on the third-party SDK, and how every task lifecycle event lands in the harness's append-only event log. Cross-organization trust (signed Agent Cards, public-network exposure with full authorization) is deliberately deferred to the `a2a-signed-cards-trust-01KQ18P9` follow-up so v1 can ship a usable, secure, local-first surface today.

## Dependencies and relationships

- **Depends on**:
  - `secrets-keychain-01KQ1A3M` — credential references for outbound auth to remote A2A peers.
  - `event-log-01KQ1A3M` — append-only event log surface for task lifecycle audit.
  - `bundle-format-resolver-01KQ1A3J` — A2A peer profiles and exposed agent definitions are bundle artifact kinds.
  - `storage-foundations-01KQ1A3K` — Tasks and A2AMessages persist in the SQLite app database.
  - `policy-engine-01KQ1A3N` — peer allowlist and per-call policy gates.
- **Coordinates with**: `llm-connector-01KQ1770` (reuses indirect credential machinery, redaction pipeline, error taxonomy); `mcp-*` (complementary — MCP is agent↔tool, A2A is agent↔agent); `shared-context-distribution-01KQ18PA` (some A2A skills will be context-distribution endpoints).
- **Blocks**: any future Python LangChain daemon mission (the daemon is an A2A peer); any multi-host workflow execution; cross-harness orchestration.
- **Does not cover**:
  - Anthropic's editor-to-agent ACP — separate mission if ever pursued.
  - Signed Agent Cards and cross-organization trust — `a2a-signed-cards-trust-01KQ18P9`.
  - The internal workflow / DAG engine that executes bundle-defined flows. A2A is the wire contract; the engine is a separate concern.
  - Public-network exposure of A2A endpoints with full authorization — v1.x.

## User Scenarios & Testing *(mandatory)*

### User Story 1 — Configure a remote A2A peer in a bundle and call it from a workflow (Priority: P1)

A bundle author declares one or more named A2A peers (each pointing at a remote agent's endpoint, with an indirect credential reference and a transport mode). A workflow node or an agent definition then targets a peer by name and a Skill name; the harness fetches the peer's Agent Card on first contact, caches it, dispatches the Task, and returns the result. The agent definition does not branch on which peer is in use — peers are swappable through configuration alone.

**Why this priority**: This is the table-stakes outbound surface. Without it, the harness cannot consume any agent in the 150+-organization A2A ecosystem and the configuration-first promise breaks the moment a workflow needs to call something outside the box.

**Independent Test**: A bundle declares one A2A peer pointing at a fixture agent on `http://localhost:<port>`. A workflow node invokes a Skill on that peer, receives the response, and the bundle author can swap the peer's endpoint to a second fixture instance with no changes outside bundle configuration.

**Acceptance Scenarios**:

1. **Given** a bundle declares a peer with an `http_loopback` transport and a working endpoint URL, **When** a workflow node invokes a named Skill on that peer, **Then** the response payload is delivered to the workflow node and a Task row plus matching event-log entries exist for the call.
2. **Given** a peer's Agent Card is fetched once and cached, **When** the same peer is invoked again within the cache TTL, **Then** the second call does not re-fetch the card and a `card_cache_hit` event is recorded.
3. **Given** a peer's credential reference points to a missing source (env var unset, keychain entry absent), **When** the runtime attempts to resolve the peer at bundle load, **Then** the operator receives an actionable startup error identifying the peer and the failed reference, before any A2A call is attempted.

---

### User Story 2 — Expose a local agent or workflow as an A2A endpoint (Priority: P1)

A bundle author marks one or more local agent definitions or workflows as "exposed over A2A." The harness generates an Agent Card from the bundle metadata, listens on a configured local transport (UDS by default on macOS/Linux, loopback HTTP on Windows), and serves inbound Tasks against the matching Skill. Other A2A clients — including a second harness instance, the future LangChain daemon, or any A2A-capable peer — can discover the card, dispatch a Task, and receive a response.

**Why this priority**: Symmetric client/server is the table-stakes posture in a 150+-organization ecosystem. Client-only would make the harness a passive consumer; server-only would be useless without peers. Both lets the harness be a first-class participant.

**Independent Test**: A test bundle declares an exposed echo agent. A standalone A2A client (the official `a2a-go` SDK in test mode) fetches the harness's Agent Card from the configured well-known path, dispatches a Task, and receives the echo response. The full lifecycle is visible in the event log.

**Acceptance Scenarios**:

1. **Given** a bundle declares an agent definition with `expose_over_a2a: true`, **When** the harness starts, **Then** an Agent Card is generated and served at the configured well-known path on the local transport.
2. **Given** an external A2A client dispatches a Task to an exposed Skill, **When** the Skill completes successfully, **Then** the response is returned to the client and inbound Task rows + event-log entries exist with `role=callee`.
3. **Given** an external client dispatches a Task to an unknown Skill, **When** the harness resolves the Skill name, **Then** it returns a typed A2A error (skill-not-found) and emits a `task_failed` event without invoking any agent code.

---

### User Story 3 — Multi-agent orchestration via A2A from a workflow node (Priority: P1)

A workflow definition includes a step that fans out to multiple A2A peers in parallel (e.g., "ask agent A and agent B for an opinion, merge the results"). The harness dispatches both Tasks concurrently, tracks their lifecycles independently, surfaces partial completion (one fails, one succeeds) to the workflow engine, and records every send/receive/state-change to the event log. Cancellation of the parent workflow propagates cancel signals to all in-flight A2A Tasks.

**Why this priority**: A2A's reason-to-exist is multi-agent orchestration. A single point-to-point call (US1) is necessary but not sufficient — real workflows fan out, merge, and cancel. Without first-class fan-out and cancellation, A2A reduces to "RPC with extra steps."

**Independent Test**: A workflow node fans out to three fixture A2A peers; one returns a result, one returns an error, one is slow enough to be cancelled. The workflow node receives a typed result-set with one success, one failure, and one cancellation, and the event log shows all three Task lifecycles.

**Acceptance Scenarios**:

1. **Given** a workflow node fans out to N peers, **When** all N Tasks are dispatched, **Then** they execute concurrently and each Task's lifecycle is independently recorded.
2. **Given** an in-flight A2A Task, **When** the parent workflow is cancelled, **Then** the harness sends an A2A cancel within one second and emits a `task_cancelled` event.
3. **Given** one of N parallel Tasks fails non-transiently, **When** the workflow node merges results, **Then** the failure is reported with its error category alongside the successful results; the workflow does not crash on the first failure.

---

### User Story 4 — Every A2A interaction is auditable through the event log (Priority: P1)

Every Task created (inbound or outbound), every Message sent or received, every state transition, every cancel, every fetched Agent Card, every authentication attempt becomes an append-only entry in the harness event log. An operator can later replay a session, audit who-called-whom-when, and confirm no credentials were ever written to disk in plaintext.

**Why this priority**: SOC 2-readiness is a charter invariant. A2A traffic is authoritative orchestration history; if it is not in the audit trail it does not exist for compliance purposes.

**Independent Test**: An operator runs a session that includes inbound and outbound A2A traffic, then queries the event log for that session and reconstructs the full Task and Message history with no plaintext credentials anywhere in the log.

**Acceptance Scenarios**:

1. **Given** an outbound A2A Task completes, **When** the operator reads the event log, **Then** entries exist for: `task_created`, `message_sent`, each `message_received`, `task_state_changed` (to running, then completed), and any `card_fetched` / `card_cache_hit` events, in the order they occurred.
2. **Given** a peer credential is resolved from a keychain entry, **When** the operator inspects the corresponding event-log entries, **Then** the resolved credential value does not appear anywhere in the log; only the credential reference (kind and lookup key) is present.
3. **Given** an A2A Task fails partway through streaming, **When** the operator reads the event log, **Then** the partial messages are present, the failure entry includes the protocol error, and the log remains internally consistent (append-only, no rewrites).

---

### User Story 5 — Local-first transport defaults keep the harness off the network (Priority: P1)

By default, A2A endpoints exposed by the harness bind to a Unix domain socket (or loopback TCP on Windows) and outbound calls only target peers whose transport is `uds` or `http_loopback`. LAN exposure (`http_lan`) requires explicit configuration; public-network exposure (`http_public`) requires both an explicit configuration escalation and a credential setup flow. The harness emits zero outbound A2A traffic until a peer is invoked.

**Why this priority**: Charter invariants: local-first, security-first, no surprise network egress. A2A is HTTP-based, which makes it trivially exposable — that's a footgun without explicit defaults that say "no."

**Independent Test**: A clean harness install with no peers configured produces zero A2A network traffic across a five-minute idle period. Adding a peer with `transport: http_public` without the required credential setup produces a configuration error at bundle load.

**Acceptance Scenarios**:

1. **Given** a fresh harness install, **When** it starts with no A2A peers configured, **Then** it emits no outbound A2A traffic and exposes no public listener.
2. **Given** a bundle declares a peer with `transport: http_public` but no `auth_ref`, **When** the bundle is loaded, **Then** the resolver rejects the peer profile with an actionable error; no outbound call is possible.
3. **Given** a local exposed agent on the default transport, **When** a non-loopback caller attempts to connect, **Then** the connection is refused at the transport layer.

---

### User Story 6 — A new A2A SDK version or transport is added without modifying the rest of core (Priority: P2)

When the `a2a-go` SDK ships a breaking change, or when a new transport (e.g., a hypothetical QUIC binding) needs to be supported, the change is contained inside `core/acp/envelope/` (SDK wrapper) or a new `core/acp/transports/<name>/` sub-package. No other `core/` package imports the SDK or knows the transport name; consumers see only the harness's A2A interfaces.

**Why this priority**: DIRECTIVE_001. Without this isolation, every A2A SDK upgrade becomes a cross-cutting refactor and the architectural-integrity invariant rots.

**Independent Test**: Bumping the `a2a-go` SDK version requires changes only in `core/acp/envelope/` (and possibly `core/acp/server/` and `core/acp/client/`). A grep across `core/` confirms no other package imports `github.com/a2aproject/a2a-go`.

**Acceptance Scenarios**:

1. **Given** an SDK minor-version bump, **When** the team upgrades, **Then** the diff is contained to the envelope package and possibly its immediate consumers; no other `core/` package needs edits.
2. **Given** an attempt to add a new transport that requires changes outside `core/acp/transports/<name>/`, **When** the change is reviewed, **Then** the review flags the architectural-integrity violation before merge.

---

### User Story 7 — Inbound A2A calls go through the same policy and audit gates as local invocations (Priority: P2)

When an external A2A client dispatches a Task to a Skill exposed by the harness, the request passes through the policy engine (peer allowlist, rate-limit, cost-ceiling where applicable) and emits the same audit events as a local invocation. A peer not on the allowlist is refused with a typed protocol error; the refusal is recorded in the event log.

**Why this priority**: Without this, exposing a Skill over A2A is an end-run around every guardrail the harness builds for local execution. It must not be possible to bypass policy by going through A2A.

**Independent Test**: A peer whose `peer_id` is not in the policy engine's A2A allowlist dispatches a Task; the harness refuses, emits a `peer_auth_failed` event, and the Skill code is never invoked.

**Acceptance Scenarios**:

1. **Given** an inbound A2A Task from an unallowlisted peer, **When** the policy gate runs, **Then** the Task is refused with a typed error and the Skill code is not invoked.
2. **Given** an inbound A2A Task from an allowlisted peer, **When** the policy gate passes, **Then** the Skill is invoked and standard audit events are emitted with `direction=inbound`.

---

### Edge Cases

- A peer's Agent Card changes between calls (version bumped on the remote): the harness detects the version mismatch on the next request, refreshes the cache, emits `card_cache_miss`, and continues.
- An outbound Task is in `awaiting_input` state when the harness restarts: on restart, the Task is reloaded from the database in `awaiting_input`; if the peer no longer recognizes the `a2a_task_id`, the Task transitions to `failed` with a typed error.
- An inbound A2A Task arrives before bundle resolution completes (race at startup): the listener is not bound until bundle load is complete; pre-bind connection attempts are refused at the transport layer.
- A peer endpoint advertises a protocol version newer than the harness's `a2a-go` SDK supports: the harness refuses to connect with a typed `unsupported_protocol_version` error at first contact, before any Task is created.
- Two peers in the same bundle use the same credential reference (same keychain entry): the secret resolver returns a single shared resolved credential per request; the credential never appears twice in the event log.
- The harness machine goes offline mid-Task (laptop closed) on an outbound call: the Task is left in `running` and will be reconciled by the scheduler / session manager on resume; no event-log entry is rewritten.
- A peer streams an SSE response and the connection drops without a terminal event: the harness treats this as a transient failure, records the partial message stream, and the Task transitions to `failed` (retry policy is the workflow's choice, not the protocol layer's).
- A Skill's `output_schema` is violated by the local agent's response: the harness emits a `task_failed` event with a typed schema-violation error and returns a protocol-level error to the caller; the malformed payload is still persisted (redacted) for audit.
- Multiple inbound Tasks share a parent session that is then cancelled: cancellation propagates to every in-flight inbound Task; each emits its own `task_cancelled` event.
- A Windows host without UDS support is configured with `transport: uds`: the bundle resolver substitutes loopback TCP automatically and emits a configuration warning; the bundle does not fail to load.

## Requirements *(mandatory)*

### Functional Requirements

| ID | Title | User Story | Priority | Status |
|----|-------|------------|----------|--------|
| FR-001 | A2A v1.0 protocol conformance | The harness implements A2A v1.0 (Linux Foundation) using the official `github.com/a2aproject/a2a-go` SDK as the wire implementation; conformance is verified against the public A2A test vectors. | High | Open |
| FR-002 | Outbound A2A client | The harness can act as an A2A client: dispatch Tasks to remote peers, send/receive Messages, observe state transitions, and cancel in-flight Tasks. | High | Open |
| FR-003 | Inbound A2A server | The harness can act as an A2A server: expose local agents and workflows as Agent Cards, accept Tasks, dispatch them to the right Skill, and return responses. | High | Open |
| FR-004 | Peer Profile bundle artifact | A2A peers are declared in bundle configuration as a new artifact kind (`a2a_peer`); bundle authors do not write code to add peers. | High | Open |
| FR-005 | Exposed-agent bundle artifact | Local agents/workflows declared as `expose_over_a2a: true` in their bundle artifact are surfaced as A2A Agent Cards at startup. | High | Open |
| FR-006 | Indirect credential resolution for peers | Peer authentication uses indirect credential references (env, keychain, file, aws_profile) per `secrets-keychain` FR-001; no plaintext keys in bundle source, the event log, or process arguments. | High | Open |
| FR-007 | Local-first transport defaults | Default transports are `uds` (macOS/Linux) and `http_loopback` (Windows). LAN (`http_lan`) requires explicit configuration. Public (`http_public`) requires both explicit configuration and a resolved `auth_ref`. | High | Open |
| FR-008 | Agent Card discovery and caching | The harness fetches a peer's Agent Card via inline configuration, well-known URL (`/.well-known/agent.json`), or manual URL; cards are cached with a configurable TTL and invalidated on version mismatch. | High | Open |
| FR-009 | Task lifecycle management | The harness tracks every A2A Task through its full lifecycle (`pending → running → awaiting_input? → completed/failed/cancelled`), persisted in the app database; state never moves backwards. | High | Open |
| FR-010 | Mid-Task cancellation | The harness can cancel an in-flight A2A Task in either role (caller or callee) and propagate the cancel signal across the wire within one second. | High | Open |
| FR-011 | Append-only event log integration | Every Task creation, message sent/received, state transition, cancel, fetched card, and auth attempt is written to the harness event log as immutable append-only entries. | High | Open |
| FR-012 | Credential and payload redaction in event log | A2A messages and event payloads pass through the same redaction pipeline as LLM events (`llm-connector` FR-015, `event-log` FR-005) before persistence. | High | Open |
| FR-013 | Policy gate for outbound calls | Every outbound A2A call passes through the policy engine's peer allowlist and per-call gates; disallowed calls emit `peer_auth_failed` and are not dispatched. | High | Open |
| FR-014 | Policy gate for inbound calls | Every inbound A2A Task passes through the policy engine's inbound allowlist; disallowed callers receive a typed protocol-level refusal and the Skill is not invoked. | High | Open |
| FR-015 | SDK isolation behind envelope wrapper | Only `core/acp/envelope/` imports `github.com/a2aproject/a2a-go`; no other `core/` package depends on the SDK directly. | High | Open |
| FR-016 | Transport extensibility | New transports are added in their own `core/acp/transports/<name>/` sub-package without modifying any other package. | Medium | Open |
| FR-017 | Multi-peer fan-out support | A workflow node may dispatch N parallel A2A Tasks; the harness tracks them concurrently and surfaces partial completion to the workflow engine. | High | Open |
| FR-018 | Pre-flight peer resolution | Every configured peer's credential reference and reachability assumption is validated at bundle load; failures surface as actionable startup errors before any agent run. | Medium | Open |
| FR-019 | Replay-friendly Task records | A persisted Task and its Messages are sufficient to reconstruct the request/response history for replay; the event log entry references the snapshot id. | Medium | Open |
| FR-020 | Verification API consumer slot | The codebase exposes a stable seam for plugging in the signed-card verification API delivered by `a2a-signed-cards-trust-01KQ18P9` without modifying the envelope or transport layers. | Medium | Open |

### Non-Functional Requirements

| ID | Title | Requirement | Category | Priority | Status |
|----|-------|-------------|----------|----------|--------|
| NFR-001 | Loopback dispatch overhead | Harness-introduced latency overhead for an outbound A2A call to a loopback peer (excluding peer execution time): under 25 ms p95 on a developer laptop. | Performance | High | Open |
| NFR-002 | Cancellation responsiveness | Time from caller-issued cancel to A2A cancel sent on the wire: under 1 second p99. | Performance | High | Open |
| NFR-003 | Event log append latency | Time from A2A event emission to append-acknowledged on local disk: under 5 ms p99 (matches charter performance target). | Performance | High | Open |
| NFR-004 | Plaintext credential leakage | Plaintext credential material appearing anywhere in the persisted event log or in A2A wire payloads at rest: zero occurrences across the audit suite. | Security | High | Open |
| NFR-005 | Local-first guarantee | The harness emits zero outbound A2A traffic when no peer is invoked, regardless of how many peers are declared. | Security | High | Open |
| NFR-006 | Default-transport binding scope | Default `uds` and `http_loopback` listeners refuse non-local connections at the transport layer; this is verified by automated tests. | Security | High | Open |
| NFR-007 | Pre-flight resolution success rate | Configured peers whose credential reference is resolvable at startup are successfully resolved 100% of the time; unresolved references are reported with the failing peer id 100% of the time. | Reliability | High | Open |
| NFR-008 | Audit completeness | Every successfully completed A2A Task produces at minimum: `task_created`, terminal `task_state_changed`, and one or more `message_sent`/`message_received` entries. Coverage: 100%. | Auditability | High | Open |
| NFR-009 | Concurrency target | The harness supports at least 32 concurrent in-flight A2A Tasks on a developer laptop without lifecycle ordering errors or event-log gaps. | Performance | Medium | Open |
| NFR-010 | SDK upgrade blast radius | A non-breaking `a2a-go` SDK upgrade requires changes only inside `core/acp/envelope/` and at most its immediate consumers; verified by static-import analysis in CI. | Maintainability | Medium | Open |

### Constraints

| ID | Title | Constraint | Category | Priority | Status |
|----|-------|------------|----------|----------|--------|
| C-001 | Architectural-integrity boundary | Only `core/acp/envelope/` imports `github.com/a2aproject/a2a-go`. The Wails app, the frontend, and any future hosted backend access A2A only via the existing `core/rpc` surface. | Technical | High | Open |
| C-002 | Append-only event log immutability | A2A-emitted event log entries are never edited or deleted in place. Corrections (e.g., post-hoc redaction expansion) are new entries that reference the prior entry. | Security | High | Open |
| C-003 | Bundle-format compatibility | A2A peer profiles and exposed-agent declarations live within the existing bundle configuration format (YAML/TOML with lockfile pinning) and do not introduce a new top-level configuration surface. | Technical | High | Open |
| C-004 | No inline plaintext credentials | Plaintext credentials are not accepted in any A2A configuration source. Only indirect credential references are accepted. | Security | High | Open |
| C-005 | Public exposure requires escalation | A peer profile with `transport: http_public` cannot be loaded without an explicit `auth_ref` and a charter-approved configuration escalation; this is enforced at bundle load. | Security | High | Open |
| C-006 | Unsigned cards over loopback/LAN only in v1 | v1 ships with unsigned Agent Cards; signed-card verification is a follow-up via `a2a-signed-cards-trust-01KQ18P9`. Public-network exposure with unsigned cards is prohibited. | Security | High | Open |
| C-007 | SOC 2-readiness | Audit, redaction, and configuration behaviors meet the testing and review bar set by the project charter. | Regulatory | High | Open |

### Key Entities

- **AgentCard**: A2A's self-description document. Carries `name`, `version`, `description`, list of supported `Skill`s, optional auth schemes, an endpoint URL, and a protocol version. The harness generates one for every locally-exposed agent and fetches+caches one for every remote peer it talks to. v1 cards are unsigned; signing arrives via the follow-up trust mission.
- **Skill**: A named capability an agent offers. Each Skill has an id, a description, a JSON-Schema `input_schema` and `output_schema`, and optional examples and tags. In the harness, a Skill maps onto either a named agent definition or a named workflow.
- **Task**: The A2A unit of work between a caller and a callee. Carries a wire-level `a2a_task_id`, a harness-local `task_id` (ULID), the local agent id, the remote peer card reference (for outbound), the targeted Skill, the harness's role (`caller` / `callee`), the lifecycle state, timestamps, and the parent session id. Persisted in the app database.
- **A2AMessage**: A single message within a Task — request body, tool-call delta, streaming chunk, or final response. Carries `direction` (inbound/outbound), `kind` (request/response/chunk/error/tool_call/tool_result), a redacted payload, and a monotonic sequence number within the Task. Append-only.
- **A2A ProviderProfile (peer)**: A bundle-declared remote A2A peer the harness intends to talk to. Carries a `peer_id`, the source of its Agent Card (inline / well-known URL / manual URL), an endpoint URL, a transport mode, an optional `auth_ref` reusing the secrets-keychain credential machinery, and a card cache TTL. Distinct from the LLM connector's `ProviderProfile` of the same name — this one is for A2A peers.
- **A2AEvent**: A typed event-log entry emitted by the A2A subsystem. Kinds include `task_created`, `message_sent`, `message_received`, `task_state_changed`, `task_cancelled`, `task_failed`, `card_fetched`, `card_cache_hit`, `card_cache_miss`, `peer_auth_attempted`, `peer_auth_failed`. Append-only; subject to the same redaction invariants as LLM events.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A bundle author can declare a remote A2A peer and invoke a Skill on it from a workflow in under 5 minutes from a clean clone, given a reachable peer endpoint and valid credentials.
- **SC-002**: A bundle author can expose a local agent or workflow over A2A with a single configuration change to the bundle (no code edit to agent definition or workflow), and a standalone A2A client successfully dispatches a Task to it.
- **SC-003**: 100% of completed A2A Tasks produce a complete, replayable event-log trail covering Task creation, every Message, every state transition, and the terminal state, verified by an audit harness that runs in CI.
- **SC-004**: Zero plaintext credentials appear in the event log, in bundle source, or in process arguments across the full A2A traffic matrix, verified by an automated scanner that runs in CI.
- **SC-005**: The harness sustains at least 32 concurrent in-flight A2A Tasks on a developer laptop without lifecycle ordering errors or event-log gaps.
- **SC-006**: A non-breaking `a2a-go` SDK upgrade is delivered with changes contained to `core/acp/envelope/` (and at most its direct consumers), verified by a structural import-graph check.
- **SC-007**: A peer profile with `transport: http_public` and no `auth_ref` is rejected at bundle load 100% of the time across the configuration matrix.
- **SC-008**: The harness emits zero outbound A2A network traffic across a five-minute idle period when no peer is invoked, verified by packet-capture instrumentation in CI.

## Assumptions

- The bundle format and lockfile mechanism described in the charter are the source of truth for A2A peer and exposed-agent declarations; this spec extends what they carry but does not redefine the format.
- The harness already provides an append-only event log, an indirect credential resolver, and a SQLite app database for persistence; this spec emits to and persists in them but does not own their internals.
- Signed Agent Cards and cross-organization trust are the responsibility of `a2a-signed-cards-trust-01KQ18P9` and will land as a follow-up. v1 ships with a stable seam (FR-020) for the verification API to plug into.
- The internal workflow / DAG engine that consumes A2A is a separate mission. This spec defines the wire surface and the per-Task lifecycle; the engine that fans out, merges, and cancels lives elsewhere.
- The Python LangChain daemon (a future mission) becomes an A2A peer; the Go harness does not import LangChain. A2A is the wire contract.
- The Wails frontend interacts with the A2A subsystem exclusively through the existing `core/rpc` surface; no new frontend-only transport is introduced.
- Anthropic's editor-to-agent ACP is explicitly out of scope and may or may not be revisited in a separate mission.

## Open Questions

These remain unresolved after research; resolving each materially shapes the implementation contract. Each has a working default; flagging them so the resolution is explicit and recorded.

1. **[NEEDS CLARIFICATION]** Default local transport on macOS/Linux — Unix domain socket or loopback TCP? UDS gives stronger isolation (filesystem permissions, no port collision) but cross-platform behavior diverges (Windows substitutes loopback TCP automatically). Default if unresolved: **UDS on macOS/Linux**, loopback TCP on Windows, with a per-bundle override available for operators who want a single transport across platforms.
2. **[NEEDS CLARIFICATION]** `a2a-go` SDK dependency model — direct module dependency, or vendored under `core/acp/envelope/vendor/`? Direct is the default Go posture; vendoring insulates the harness from sudden upstream breakage and makes the enterprise build's reproducibility story easier. Default if unresolved: **direct module dependency**, pinned to a specific minor version, with a CI gate that flags major-version bumps for explicit review.
3. **[NEEDS CLARIFICATION]** Default Agent Card cache TTL — how long should remote cards be trusted before re-fetch? Too short increases card-fetch overhead; too long delays propagation of remote agent updates. Default if unresolved: **300 seconds (5 minutes)**, configurable per-peer via the `card_cache_ttl_s` field on `ProviderProfile`.
