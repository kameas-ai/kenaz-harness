# Data Model (Discovery Draft) — A2A Orchestration

This captures the A2A v1.0 concepts kaneaz-harness needs to model, plus the harness-side entities that wrap them. Implementation and planning will refine types, field names, and storage.

## Entities

### Entity: AgentCard

- **Description**: A2A's self-description document that an agent publishes so other agents can discover it. The standard carries capability, skill, and (optionally) authentication metadata. In the harness, every agent definition that we expose over A2A publishes an AgentCard; every external agent we speak to has one we fetch and cache.
- **Attributes**:
  - `agent_id` (string, unique per local instance) — harness-assigned stable identifier.
  - `name` (string) — human-readable.
  - `description` (string) — natural-language purpose.
  - `version` (semver string) — agent version (distinct from A2A protocol version).
  - `skills` (list of Skill IDs) — what this agent can do.
  - `auth_schemes` (list, optional) — supported authentication (empty in v1 loopback-only deployments).
  - `signed` (bool) — whether the card is signed; v1 defaults to `false`, signing deferred per D7.
  - `endpoint_url` (URL) — where the agent accepts A2A requests (UDS path or `http://localhost:<port>` in v1).
  - `protocol_version` (string) — A2A protocol version this agent implements (e.g., `1.0`).
- **Identifiers**: `agent_id` (harness-local); the A2A card's own `name`+`version` tuple for remote cards.
- **Lifecycle Notes**: Local cards are generated when a bundle is loaded and re-generated on bundle reload. Remote cards are fetched lazily on first contact and cached; cache entries invalidate when the remote returns a newer version or after a configurable TTL.

### Entity: Skill

- **Description**: A named capability an agent offers, as defined by A2A. In the harness, a Skill corresponds to either a named agent definition in a bundle or a named workflow in the workflow engine.
- **Attributes**:
  - `skill_id` (string) — unique within the agent.
  - `name` (string)
  - `description` (string)
  - `input_schema` (JSON Schema) — expected inputs.
  - `output_schema` (JSON Schema) — expected outputs.
  - `examples` (list, optional) — hints for discovery.
  - `tags` (list of strings, optional) — for routing / filtering.
- **Identifiers**: `(agent_id, skill_id)`.
- **Lifecycle Notes**: Immutable once published; change the `version` on the parent AgentCard to ship a schema change.

### Entity: Task

- **Description**: A2A's unit of work between a caller (client role) and a callee (agent role). Tasks carry a message history and a lifecycle state. In the harness, every inbound A2A call creates a Task; every outbound call to a remote agent creates a peer Task we track.
- **Attributes**:
  - `task_id` (ULID) — harness-assigned.
  - `a2a_task_id` (string) — the wire-level A2A task identifier.
  - `local_agent_id` (string) — which local agent owns this task (for inbound) or is making the call (for outbound).
  - `remote_agent_card` (reference) — the peer AgentCard for outbound tasks; null for inbound where we are the callee.
  - `skill_id` (string) — which Skill is being invoked.
  - `role` (enum: `caller`, `callee`) — our side of the conversation.
  - `state` (enum: `pending`, `running`, `awaiting_input`, `completed`, `failed`, `cancelled`) — mapped from A2A task states.
  - `created_at` / `updated_at` (timestamp)
  - `parent_session_id` (reference, optional) — harness session that initiated or owns this task.
- **Identifiers**: `task_id` (harness), `a2a_task_id` (wire).
- **Lifecycle Notes**: Tasks are first-class rows persisted in the harness app database (see `storage-foundations` mission). Every state transition emits an append-only LLM/A2A event (see Event Log below) for replay and audit. Cancellation propagates symmetrically in both roles.

### Entity: A2AMessage

- **Description**: A single message within a Task — request payload, tool-call delta, streaming chunk, or final response. Structurally mirrors A2A's `Message` with harness metadata.
- **Attributes**:
  - `message_id` (ULID)
  - `task_id` (reference) — parent Task.
  - `direction` (enum: `inbound`, `outbound`) — from the harness's point of view.
  - `kind` (enum: `request`, `response`, `chunk`, `error`, `tool_call`, `tool_result`) — shape hint.
  - `payload` (blob, redacted) — the message body. Credential-pattern redaction is applied before persistence per llm-connector FR-015.
  - `sequence` (int) — order within the task.
  - `emitted_at` (timestamp)
- **Identifiers**: `message_id`; `(task_id, sequence)` for replay.
- **Lifecycle Notes**: Append-only. Corrections (e.g., post-hoc redaction expansion) are new messages that reference the original.

### Entity: ProviderProfile (local-first peer registry)

- **Description**: The harness-side declaration of a remote A2A agent we intend to talk to. Lives in bundle configuration, not in code. Distinct from the LLM connector's `ProviderProfile` — this one is for A2A peers, not LLM providers.
- **Attributes**:
  - `peer_id` (string) — stable reference used in workflow definitions.
  - `agent_card_source` (enum + location) — one of: `inline` (card embedded in bundle), `well_known_url` (fetch `/.well-known/agent.json` at the endpoint), `manual_url` (fetched from a configured URL).
  - `endpoint_url` (URL) — where to reach the peer.
  - `transport` (enum: `uds`, `http_loopback`, `http_lan`, `http_public`) — v1 defaults restrict to `uds` or `http_loopback`; `http_public` requires an explicit charter-approved credential setup.
  - `auth_ref` (CredentialReference, optional) — reuses the llm-connector's indirect credential reference machinery.
  - `card_cache_ttl_s` (int, default 300)
- **Identifiers**: `peer_id` (bundle-scoped).
- **Lifecycle Notes**: Resolved at bundle load (pre-flight per llm-connector FR-019). Unresolvable peers surface startup errors rather than runtime failures.

### Entity: A2AEvent (in the harness event log)

- **Description**: Typed event log entries emitted by the A2A adapter into the existing harness append-only event log (shared with LLM connector, MCP, scheduler).
- **Attributes**:
  - `event_id` (ULID)
  - `session_id` (reference)
  - `task_id` (reference) — which A2A Task this event belongs to.
  - `kind` (enum: `task_created`, `message_sent`, `message_received`, `task_state_changed`, `task_cancelled`, `task_failed`, `card_fetched`, `card_cache_hit`, `card_cache_miss`, `peer_auth_attempted`, `peer_auth_failed`)
  - `payload_ref` (blob reference) — redacted event body.
  - `emitted_at` (timestamp)
- **Identifiers**: `event_id`.
- **Lifecycle Notes**: Append-only. Never edited in place. All the same redaction invariants as LLM connector events (charter C-003, llm-connector FR-015).

## Relationships

| Source | Relation | Target | Cardinality | Notes |
|---|---|---|---|---|
| Bundle | **declares** | AgentCard (local) | 1:N | One bundle can expose multiple agents over A2A. |
| AgentCard | **advertises** | Skill | 1:N | Every exposed capability is a Skill. |
| Workflow definition (separate mission) | **fulfills** | Skill | 1:1 or N:1 | A workflow implements one or more Skills. |
| Bundle | **declares** | ProviderProfile (A2A peer) | 1:N | Bundle-scoped registry of known remote agents. |
| ProviderProfile | **references** | AgentCard (remote, cached) | 1:1 | Cached card backing the peer. |
| Session | **owns** | Task | 1:N | A session may issue or receive multiple tasks. |
| Task | **contains** | A2AMessage | 1:N | Ordered message history. |
| Task | **emits** | A2AEvent | 1:N | Audit trail for every task lifecycle event. |
| Task (outbound) | **targets** | AgentCard (remote) | N:1 | Peer whose Skill is being invoked. |
| Task (inbound) | **executes_against** | AgentCard (local) | N:1 | Local agent whose Skill is being invoked. |
| CredentialReference (llm-connector) | **authenticates** | ProviderProfile (A2A peer) | 1:N | Reused machinery; no new plaintext-key surface. |

## Validation & Governance

- **Data quality requirements**:
  - AgentCards MUST have non-empty `agent_id`, `name`, and at least one Skill.
  - Skills MUST have valid JSON Schemas for both input and output.
  - Tasks MUST NOT transition states backwards; `cancelled` and `failed` are terminal alongside `completed`.
  - A2AMessages within a Task are strictly append-only and monotonic by `sequence`.
  - Every Task with `role=caller` MUST carry a resolved `remote_agent_card` before any message is sent.
- **Compliance considerations**:
  - Credentials pass through the indirect-reference machinery defined in llm-connector FR-003; no plaintext keys in AgentCards, ProviderProfiles, or the event log.
  - A2AMessages and A2AEvents persisted to disk are subject to the same redaction pipeline as LLM events (llm-connector FR-015, NFR-007, NFR-008).
  - All task lifecycle events persist to the append-only event log (charter C-003, llm-connector FR-014).
  - Public-network exposure (ProviderProfile `transport=http_public`) is a configuration escalation that requires the operator-facing warning and credential setup flow (planning-phase design).
- **Source of truth**:
  - Bundle configuration is the source of truth for which agents we expose and which peers we can reach.
  - Remote AgentCards are *fetched and cached*, never hand-edited; the remote endpoint is authoritative.
  - Task and A2AMessage state live in the harness app database (SQLite, per `storage-foundations` mission).
  - The harness event log is the source of truth for auditability and replay.

> Treat this as a working model. When A2A publishes its formal auth scheme (currently "on roadmap"), or when IBM/Cisco/Google ship widely adopted extensions, update the ProviderProfile and AgentCard entities to match rather than invent parallel types.
