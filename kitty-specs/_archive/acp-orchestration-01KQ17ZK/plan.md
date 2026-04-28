# Implementation Plan: ACP Orchestration — A2A Agent-to-Agent Protocol Surface

**Mission**: `acp-orchestration-01KQ17ZK`
**Spec**: `kitty-specs/acp-orchestration-01KQ17ZK/spec.md`
**Branch contract (from `setup-plan`)**:
- Feature branch: `feat/acp-orchestration-01KQ17ZK`
- Planning base / merge target: `main` (per `meta.json` + charter Branch Strategy)
- All changes ship via PR; squash-merge default; ≥ 1 maintainer review.

> Branch contract restated: feature work lands on `feat/acp-orchestration-01KQ17ZK`,
> targets `main`, and ships via PR. No direct push to `main` under any
> circumstance.

---

## 1. Overview

This plan turns the ACP-orchestration spec into a concrete Go architecture
inside `core/acp/`. The package is the harness's single interface to the
**A2A v1.0** agent-to-agent protocol (Linux Foundation, released 2026-03-12)
and wraps the official `github.com/a2aproject/a2a-go` SDK behind an internal
envelope so the rest of `core/` never depends on the third-party SDK
(DIRECTIVE_001).

Bounding scope (v1):

- A2A v1.0 conformance via `a2a-go`, both client and server roles (FR-001 –
  FR-003).
- Bundle-declared peers and exposed local agents/workflows (FR-004, FR-005).
- Indirect credential resolution reusing `secrets-keychain` machinery
  (FR-006).
- Local-first transport defaults: `uds` (mac/Linux), `http_loopback`
  (Windows), opt-in `http_lan`, escalated `http_public` (FR-007, NFR-005,
  NFR-006).
- Agent Card discovery + cache (FR-008).
- Full Task lifecycle persistence and cancellation (FR-009, FR-010).
- Append-only audit through the existing event log with redaction
  (FR-011, FR-012).
- Policy gates inbound and outbound (FR-013, FR-014).
- Multi-peer fan-out (FR-017).
- Pre-flight peer resolution (FR-018) and replay-friendly Task records
  (FR-019).
- Stable seam for the future signed-card verification API (FR-020).

Explicit non-goals for v1 (per spec Assumptions):

- Signed Agent Cards / cross-organization trust — owned by
  `a2a-signed-cards-trust-01KQ18P9`.
- Public-network exposure with full authorization — v1.x.
- Workflow / DAG engine — separate mission; this plan provides the per-Task
  surface a fan-out engine will consume.
- Anthropic's editor-to-agent ACP — out of scope.

---

## 2. Architectural Placement

The A2A subsystem lives at `core/acp/`. The Wails app, the frontend, and any
future hosted backend reach it only through `core/rpc` (charter
DIRECTIVE_001; spec C-001). Subpackages keep transports, SDK wrappers, and
persistence concerns isolated so each can evolve independently.

```
core/acp/
├── acp.go                  # Public types: AgentCard, Skill, Task, Message,
│                           # PeerRegistry, A2AClient, A2AServer interfaces,
│                           # error taxonomy. Re-exports the harness-shaped
│                           # contract; never imports a2a-go.
├── peers/                  # Bundle-declared peer registry (FR-004).
│                           # Loads ProviderProfile (peer) entries from the
│                           # bundle resolver, validates them, and resolves
│                           # credential refs via core/secrets at preflight.
├── server/                 # Inbound A2A endpoint (FR-003, FR-005, FR-014).
│                           # Listens on a configured transport, dispatches
│                           # incoming Tasks to local Skills, runs the policy
│                           # gate before invocation.
├── client/                 # Outbound A2A dispatch (FR-002, FR-013, FR-017).
│                           # Looks up peer, runs policy gate, resolves
│                           # credentials, builds an A2A Task, hands off to
│                           # envelope, tracks lifecycle, emits events.
├── envelope/               # SOLE importer of github.com/a2aproject/a2a-go
│                           # (FR-015, NFR-010, C-001). Translates between
│                           # core/acp.* types and SDK types. The seam for
│                           # SDK upgrades.
├── transports/
│   ├── uds/                # Unix domain socket listener + dialer (default
│   │                       # macOS/Linux). Filesystem permissions used as
│   │                       # the access control surface.
│   ├── http_loopback/      # 127.0.0.1:<ephemeral> listener + dialer
│   │                       # (default on Windows; opt-in elsewhere).
│   ├── http_lan/           # Bind to a non-loopback local interface;
│   │                       # opt-in only.
│   └── http_public/        # WAN-bound listener / dialer; v1.x; refuses to
│                           # load without a resolved auth_ref (C-005).
├── events/                 # Typed event-log emit helpers for A2A kinds
│                           # (FR-011, FR-012). Single chokepoint to
│                           # core/event with redaction-aware payload
│                           # builders.
├── store/                  # Persistence adapter for Tasks and A2AMessages
│                           # against core/storage (storage-foundations).
│                           # Owns nothing schema-wise that the storage
│                           # mission doesn't already provide.
├── verify/                 # FR-020 stable seam: a no-op Verifier interface
│                           # plus default UnsignedAcceptVerifier. The
│                           # signed-card mission swaps in the real impl.
└── internal/               # shared helpers (skill router, ID minting, ULID
                            # generation) usable only inside core/acp/.
```

Architectural-integrity invariants:

- Only `core/acp/envelope/` imports `github.com/a2aproject/a2a-go`
  (FR-015, NFR-010, C-001). CI enforces this with a static-import lint
  rule.
- Each transport sub-package is self-contained. Adding a transport never
  touches `client/` or `server/` code paths beyond a registration call
  (FR-016).
- `core/acp/acp.go` defines the public adapter contract; subpackages depend
  on it but it does not depend on them.
- Wails / RPC / UI never import `core/acp/<sub>/` directly — they go via
  `core/acp` through `core/rpc`.

---

## 3. Public API (Illustrative Signatures)

These signatures are illustrative — they shape, but do not freeze, the
implementation. Detail lands in tasks.

```go
package acp

// TransportKind enumerates supported transports (FR-007, FR-016).
type TransportKind string

const (
    TransportUDS         TransportKind = "uds"
    TransportLoopback    TransportKind = "http_loopback"
    TransportLAN         TransportKind = "http_lan"
    TransportPublic      TransportKind = "http_public"
)

// Skill is a named capability advertised by an agent (FR-005).
type Skill struct {
    ID           string
    Name         string
    Description  string
    InputSchema  json.RawMessage // JSON Schema
    OutputSchema json.RawMessage
    Examples     []json.RawMessage
    Tags         []string
}

// AgentCard is A2A's self-description (FR-008).
type AgentCard struct {
    AgentID         string
    Name            string
    Description     string
    Version         string
    ProtocolVersion string // "1.0"
    EndpointURL     string
    Skills          []Skill
    AuthSchemes     []string
    Signed          bool   // v1: always false; verify/ swaps later
}

// PeerProfile is a bundle-declared remote peer (FR-004, FR-006, FR-007).
type PeerProfile struct {
    PeerID          string
    EndpointURL     string
    Transport       TransportKind
    CardSource      CardSource              // inline / well_known / manual_url
    InlineCard      *AgentCard              // when CardSource == inline
    AuthRef         *secrets.Reference      // optional; required for http_public
    CardCacheTTL    time.Duration           // default 5 minutes
}

// TaskRole is the harness's role on a Task (FR-009).
type TaskRole string

const (
    RoleCaller TaskRole = "caller"
    RoleCallee TaskRole = "callee"
)

// TaskState mirrors A2A states (FR-009).
type TaskState string

const (
    StatePending        TaskState = "pending"
    StateRunning        TaskState = "running"
    StateAwaitingInput  TaskState = "awaiting_input"
    StateCompleted      TaskState = "completed"
    StateFailed         TaskState = "failed"
    StateCancelled      TaskState = "cancelled"
)

// Task is the harness-side persisted view of an A2A task.
type Task struct {
    TaskID           string    // ULID, harness-local
    A2ATaskID        string    // wire-level
    LocalAgentID     string
    RemotePeerID     string    // empty for inbound
    SkillID          string
    Role             TaskRole
    State            TaskState
    ParentSessionID  string
    SnapshotID       string    // resolver snapshot, for replay (FR-019)
    CreatedAt        time.Time
    UpdatedAt        time.Time
}

// Message is one entry in a Task's message stream.
type Message struct {
    MessageID  string
    TaskID     string
    Direction  string // "inbound" | "outbound"
    Kind       string // "request"|"response"|"chunk"|"error"|"tool_call"|"tool_result"
    Payload    json.RawMessage // redacted at persistence time (FR-012)
    Sequence   int
    EmittedAt  time.Time
}

// PeerRegistry is the materialized peer set (FR-004, FR-018).
type PeerRegistry interface {
    Load(profiles []PeerProfile) error
    Lookup(peerID string) (PeerProfile, error)
    All() []PeerProfile
    PreflightAll(ctx context.Context) []PreflightResult
}

// A2AClient is the outbound surface (FR-002, FR-010, FR-017).
type A2AClient interface {
    Dispatch(ctx context.Context, peerID, skillID string, body json.RawMessage,
             opts ...DispatchOption) (Task, <-chan Message, error)
    Cancel(ctx context.Context, taskID string) error
}

// A2AServer is the inbound surface (FR-003, FR-014).
type A2AServer interface {
    Expose(card AgentCard, dispatcher SkillDispatcher) error
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
}

// SkillDispatcher is what the inbound server calls when a Task arrives.
type SkillDispatcher interface {
    Dispatch(ctx context.Context, task Task, in json.RawMessage) (out json.RawMessage, err error)
}

// Verifier is the FR-020 stable seam for signed-card verification.
type Verifier interface {
    Verify(ctx context.Context, card AgentCard) (VerifyResult, error)
}
```

Errors form a typed taxonomy (FR-007 escalation, FR-013/014 policy):
`ErrPeerNotFound`, `ErrCredentialResolution`, `ErrPolicyDenied`,
`ErrSkillNotFound`, `ErrUnsupportedProtocolVersion`, `ErrTransportRefused`,
`ErrCancelled`, `ErrTaskFailed`, `ErrSchemaViolation`. Subpackages classify;
the public surface returns the typed errors verbatim.

---

## 4. Internal Layering

### Outbound flow (caller side)

```
Caller (workflow node / agent / rpc)
  └─→ A2AClient.Dispatch
        ├─ PeerRegistry.Lookup(peerID)            (FR-004)
        ├─ PolicyGuard.AllowOutbound(peer, skill) (FR-013)
        ├─ CredentialResolver.Resolve(peer.AuthRef)
        │     via core/secrets.Backend.Resolve    (FR-006)
        ├─ AgentCardCache.Get(peer)               (FR-008)
        │     miss → envelope.FetchCard → cache
        │     emit card_fetched / card_cache_*    (FR-011)
        ├─ Verifier.Verify(card)                  (FR-020 seam — no-op v1)
        ├─ Events.TaskCreated(task)               (FR-011)
        ├─ envelope.Dispatch(task, body)          (only place a2a-go runs)
        │     └─ transports/<kind>.Dial → SDK send/receive
        ├─ stream of Messages persisted to store/ (FR-009, FR-019)
        │     each emits message_sent/received    (FR-011, FR-012)
        ├─ on cancel: Events.TaskCancelled +
        │     envelope.CancelTask(a2a_task_id)    (FR-010)
        └─ terminal state → Events.TaskStateChanged
              + Events.TaskFailed where applicable
```

### Inbound flow (callee side)

```
External A2A client
  └─→ transport listener → envelope.AcceptTask
        ├─ PolicyGuard.AllowInbound(peer-id, skill) (FR-014)
        │     refusal → typed protocol error + peer_auth_failed
        ├─ Verifier.Verify(claimed-card)            (FR-020 seam)
        ├─ Events.TaskCreated(task, role=callee)
        ├─ SkillRouter.Resolve(skill_id)            (FR-005)
        │     unknown → ErrSkillNotFound + task_failed
        ├─ store/.Persist(task)                     (FR-009)
        ├─ SkillDispatcher.Dispatch(...)            (caller-supplied)
        │     output schema validated against Skill
        ├─ envelope.Respond(task, message)
        └─ terminal Events.TaskStateChanged
```

### Layers in detail

- **Envelope** (`core/acp/envelope/`, FR-015, NFR-010): the only importer
  of `a2a-go`. Translates between `core/acp.*` types and SDK request/response
  shapes; handles SDK-level streaming primitives. The seam for SDK upgrades
  — see §8 R1.

- **Transports** (`core/acp/transports/<kind>/`, FR-007, FR-016, NFR-006):
  each transport implements `Dial(ctx, endpoint) (net.Conn, error)` and
  `Listen(ctx, addr) (net.Listener, error)`. The default-binding-scope
  invariant (NFR-006) is enforced inside each transport package: `uds`
  refuses non-local file paths; `http_loopback` binds only to `127.0.0.1` /
  `::1`; `http_public` refuses to construct without an `auth_ref` resolved
  successfully via `core/secrets`. Windows automatically substitutes
  `http_loopback` for `uds` per spec edge case.

- **PeerRegistry** (`core/acp/peers/`, FR-004, FR-018): in-memory map of
  `peer_id → PeerProfile` populated by the bundle resolver's
  `ArtifactKindHandler` (see §6.3). `PreflightAll` resolves every
  `AuthRef` (success/failure) and emits `peer_auth_attempted`/
  `peer_auth_failed` events at startup; failures keyed by `peer_id` block
  bundle activation.

- **AgentCardCache** (inside `core/acp/peers/`, FR-008): keyed by `peer_id`
  with TTL-based expiry. Cache misses fetch via envelope; cache hits emit
  `card_cache_hit`. Version mismatch on a fresh fetch invalidates the
  cached entry and emits `card_cache_miss`. Default TTL: 300 s (Open
  Question 3 default).

- **PolicyGuard** (FR-013, FR-014): two gate methods, `AllowOutbound` and
  `AllowInbound`. Backed by the policy-engine mission once it lands; until
  then, a no-op default Allow-all impl with a CI alarm if it is still
  in use at v1 release.

- **SkillRouter** (`core/acp/internal/skillrouter`, FR-005): maps incoming
  `(agent_id, skill_id)` to a registered `SkillDispatcher`. Registration
  comes from the bundle resolver activating exposed-agent artifacts.

- **Events** (`core/acp/events/`, FR-011, FR-012): single chokepoint to
  `core/event.Log.Append`. Every payload passes through redaction (the
  event-log pipeline, not this package's responsibility) before
  persistence. `core/acp/` itself never logs a resolved credential —
  redaction is defense-in-depth.

- **store/** (FR-009, FR-019): adapter over `core/storage` for the
  `acp_tasks` and `acp_messages` tables. Append-only on messages; state
  transitions on tasks emit a paired `task_state_changed` event with
  before/after state.

- **Verifier** (`core/acp/verify/`, FR-020): `Verifier` interface with a
  default `UnsignedAcceptVerifier` shipped in v1. `a2a-signed-cards-trust`
  delivers an `X509Verifier` (or whatever it lands as) that drops in via
  one binding change.

---

## 5. Data Model

### 5.1 Bundle artifacts

Two new artifact kinds register with the bundle resolver, both stored as
YAML inside the bundle. No new top-level configuration surface (C-003).

**`a2a_peer`** — outbound peer declaration:

```yaml
artifacts:
  - kind: a2a_peer
    name: research-helper
    path: peers/research-helper.yaml

# peers/research-helper.yaml
schema_version: 1
peer_id: research-helper
endpoint_url: http://localhost:9100
transport: http_loopback           # uds | http_loopback | http_lan | http_public
card_source: well_known            # inline | well_known | manual_url
card_cache_ttl_s: 300
auth:                              # required for http_public; optional otherwise
  keychain: kaneaz/a2a-research-helper
```

**`a2a_exposed_agent`** — inbound exposure declaration. Reuses the
existing agent-definition artifact, opting in via `expose_over_a2a: true`
and an explicit `skills:` block:

```yaml
schema_version: 1
agent_id: docwriter
expose_over_a2a: true
skills:
  - id: write_release_notes
    name: Write Release Notes
    input_schema_path: schemas/release-notes-input.json
    output_schema_path: schemas/release-notes-output.json
```

Validation rules (`core/acp/peers/schema.go`):

- `peer_id` unique within the harness's resolved peer set; alphanumeric
  with dash/underscore only.
- `transport` ∈ {`uds`, `http_loopback`, `http_lan`, `http_public`}; on
  Windows hosts, `uds` is rewritten to `http_loopback` with a warning.
- `transport: http_public` REQUIRES `auth` to be present and resolvable
  (C-005); refusal at bundle load (US5 Acceptance 2).
- `card_source: inline` requires an `inline_card:` field with at minimum
  `name`, `endpoint_url`, and one Skill.

### 5.2 Persistence (consumes `storage-foundations`)

Two tables, owned by this mission, sitting in the harness app database:

- `acp_tasks` — one row per Task. Columns: `task_id` (PK, ULID),
  `a2a_task_id`, `local_agent_id`, `remote_peer_id`, `skill_id`, `role`,
  `state`, `parent_session_id`, `snapshot_id`, `created_at`, `updated_at`.
  Index on `(parent_session_id, created_at)` for replay; index on
  `(state)` for in-flight cancellation sweeps.
- `acp_messages` — one row per Message, append-only. Columns:
  `message_id` (PK, ULID), `task_id` (FK), `direction`, `kind`, `sequence`,
  `payload_blob_ref`, `emitted_at`. Unique index on
  `(task_id, sequence)`.

Migrations register through the storage-foundations migration framework
(`storage-foundations` US3). The `payload_blob_ref` indirects through
`core/blob` to keep large message bodies out of the relational table.

### 5.3 Event-log kinds emitted by the A2A subsystem

Namespaced `acp/` per `event-log` FR-017. Each carries `session_id`,
`emitter_id="acp/<role>"`, `task_id`, `peer_id` (where applicable), and a
redacted payload.

| Kind | Purpose | FR coverage |
|---|---|---|
| `acp/task_created` | new task entered the system | FR-011, NFR-008 |
| `acp/message_sent` | wire send | FR-011, FR-012 |
| `acp/message_received` | wire receive | FR-011, FR-012 |
| `acp/task_state_changed` | lifecycle transition (before → after) | FR-009, FR-011 |
| `acp/task_cancelled` | cancellation propagated | FR-010 |
| `acp/task_failed` | terminal failure with error category | FR-011 |
| `acp/card_fetched` | new Agent Card retrieved | FR-008 |
| `acp/card_cache_hit` | served from cache | FR-008 |
| `acp/card_cache_miss` | invalidated and refetched | FR-008 |
| `acp/peer_auth_attempted` | credential resolved + about to use | FR-013 |
| `acp/peer_auth_failed` | resolve fail or policy refusal | FR-013, FR-014 |

Per US4 Acceptance 1: a successful outbound Task produces at minimum
`task_created` → ≥ 1 × `message_sent`/`message_received` →
`task_state_changed` (running) → `task_state_changed` (completed).

### 5.4 Replay determinism

Every `task_created` payload includes the `ResolvedGraph.snapshot_id`
(bundle resolver) under which the call was issued, so replay can
reconstruct the exact peer profile + exposed-agent state of the original
run (FR-019). This piggybacks on the same machinery the LLM connector uses
(llm-connector FR-020).

---

## 6. Integration Points

### 6.1 secrets-keychain-01KQ1A3M

- The A2A client calls `core/secrets.Backend.Resolve(ref)` at request time
  for any peer with a non-nil `AuthRef` (FR-006, secrets FR-002).
- Resolved bytes live in a `core/secrets.Secret` (`[]byte`-typed; never
  `string`) and are zeroized once the envelope has built the wire request
  (secrets FR-013).
- Pre-flight (FR-018) calls `core/secrets.PreflightAll` for every peer's
  `AuthRef`. Failures map to `acp/peer_auth_failed` events keyed by
  `peer_id`.
- Reference kinds reused verbatim: `env`, `keychain`, `file`, `aws_profile`,
  `kms`. No new reference kind is introduced by this mission.

### 6.2 event-log-01KQ1A3M

- All emit goes through `core/event.Log.Append` (event-log FR-001).
- Event kinds registered under `acp/` namespace (event-log FR-017).
- Redaction is the event-log pipeline's responsibility (event-log FR-005,
  FR-006). The A2A subsystem's contract: never put resolved credentials
  into the payload in the first place; never log raw HTTP request bodies.
- Payload references go through `core/blob` for large message bodies; the
  log entry carries only the blob reference.

### 6.3 bundle-format-resolver-01KQ1A3J

- The A2A subsystem registers two `ArtifactKindHandler`s at process start
  (bundle FR-002, FR-005): one for `kind: a2a_peer`, one for the
  `expose_over_a2a` extension on existing agent-definition artifacts.
- Handler signatures follow the upstream contract: `Parse(bytes)→Profile`,
  `Validate(Profile, ManifestCtx)→errors`, `Activate(Profile,
  ResolverCtx)→registration` (in `PeerRegistry` and `SkillRouter`
  respectively).
- Activation order is deterministic per `ResolvedGraph.activation_order`.
  Peer-id collisions across team + personal overlays are surfaced through
  the resolver's `FR-009` conflict-detection path.

### 6.4 storage-foundations-01KQ1A3K

- The A2A `store/` adapter consumes the storage-foundations transactional
  surface for `acp_tasks` and `acp_messages` writes.
- Migrations register with the migration framework (storage-foundations
  US3). One migration ships in v1 to create both tables and their indexes.
- Online backup (storage-foundations US5) covers A2A state by virtue of
  living in the same DB file.

### 6.5 policy-engine-01KQ1A3N

The A2A subsystem is a heavy consumer of the policy engine. Integration
touches three points:

- **Peer registration** — on `PeerRegistry.Load`, the policy engine's
  `A2A.Allowlist(peer_id)` is consulted; disallowed peers are not
  registered and emit `acp/peer_auth_failed` (US7 Acceptance 1).
- **Outbound per-call gate** — `PolicyGuard.AllowOutbound(peer, skill)`
  enforces per-peer rate limits, per-skill restrictions, and any global
  cost ceilings.
- **Inbound per-call gate** — `PolicyGuard.AllowInbound(claimed_peer,
  skill)` enforces an inbound allowlist (US7 Acceptance 1, FR-014).

The policy engine itself is a separate mission. v1 ships with a no-op
`PolicyGuard` returning `Allow` from all methods, marked `// TODO:
policy-engine` so a CI lint catches it before release.

### 6.6 a2a-signed-cards-trust-01KQ18P9 (consumer slot)

- `core/acp/verify/Verifier` is the seam (FR-020).
- v1 ships `UnsignedAcceptVerifier` as the default; it accepts every
  fetched card and never rejects.
- The follow-up mission delivers a real `Verifier` impl that the harness
  binds in via configuration; no envelope or transport changes required.

### 6.7 core/rpc

The Wails frontend reaches the A2A subsystem exclusively via the existing
`core/rpc` surface (charter; spec C-001). v1 RPC methods envisioned
(defined in tasks, not here):

- `ACP.ListPeers() → [PeerSummary]`
- `ACP.ListExposedAgents() → [ExposedAgent]`
- `ACP.DispatchTask(req) → TaskHandle` (server-streamed messages)
- `ACP.CancelTask(taskID)`
- `ACP.PreflightPeers() → [PreflightResult]`
- `ACP.GetTask(taskID) → TaskDetail` (replay)

---

## 7. Phasing

### v1.0 — this mission

Scope:

- `core/acp/` skeleton with the layout in §2.
- All four transports (`uds`, `http_loopback`, `http_lan`, `http_public`),
  with `http_public` refusing to construct without `auth_ref` resolved
  (C-005).
- Envelope wrapping `a2a-go`, pinned to a specific minor version, with the
  CI lint rule that blocks `a2a-go` imports outside `envelope/`.
- `PeerRegistry`, `A2AClient`, `A2AServer`, `SkillRouter`, `Events`,
  `store/`, `verify/UnsignedAcceptVerifier`.
- Bundle artifact kinds `a2a_peer` and `expose_over_a2a` extension.
- Pre-flight peer resolution at bundle load with actionable startup
  errors.
- Audit emit for every event kind in §5.3.
- Replay-friendly Task records with snapshot_id pins.
- Test coverage: ≥ 80% `core/acp/**` line (charter target); black-box
  integration tests using the SDK's test-server primitives plus a fixture
  harness instance for symmetric client+server tests.

### v1.x — fast-follows (separate missions)

- **Signed Agent Cards / cross-org trust** (`a2a-signed-cards-trust-
  01KQ18P9`): drops in a real `Verifier` and adds key-management plumbing.
  No envelope changes expected.
- **Public-network exposure with full authorization**: extends
  `transports/http_public/` and the policy engine's outbound gate to
  enforce a charter-approved auth flow. Likely depends on the trust
  mission landing first.
- **Python LangChain daemon mission**: spawns the daemon, exposes its
  workflows as A2A peers, registers them through the same bundle-artifact
  surface. No envelope changes; this mission's only contribution is the
  peer-profile shape.

### v2 — out of scope this spec

- Formal A2A authorization scheme conformance once the spec stabilizes
  (research note: "authorization schemes embedded in AgentCard are still
  being formalized"). When that lands upstream, the `auth_schemes` field
  on `AgentCard` becomes load-bearing and `verify/` gains a
  `SupportedSchemes()` method.
- Cross-harness multi-host orchestration with discovery (e.g., A2A
  registry / catalog services). v1 and v1.x rely on bundle-declared
  peers.

---

## 8. Risk Register

Premortem-driven (Charter Tactic `premortem-risk-identification`). Top
failure modes and mitigations:

| # | Risk | Impact | Likelihood | Mitigation |
|---|---|---|---|---|
| R1 | `a2a-go` SDK API churn (it is a young first-party SDK; v1.0 of A2A is recent). A breaking change leaks past `envelope/` and forces edits across the harness. | High — collapses DIRECTIVE_001 / FR-015 invariant; every bump becomes a project. | Medium-high in the first 12 months. | Pin to a specific minor version; CI lint blocks `a2a-go` imports outside `envelope/`. Envelope deliberately re-types every SDK struct in `core/acp/*` types so downstream packages never see SDK shapes. ADR captures the upgrade procedure. |
| R2 | A2A spec drift between v1.0 and v1.x (authorization scheme finalization, signed-card semantics). Harness implements an interpretation that diverges from the eventually-formalized spec. | Medium — interop bugs with downstream peers. | Medium — charter-aligned with deferring trust to follow-up, but the protocol layer will still see updates. | Track A2A spec releases via the source register; gate any spec-version-affecting change behind an ADR; conformance tests run against the official A2A test vectors when published. |
| R3 | UDS-vs-localhost-TCP cross-platform divergence: a bundle works on Linux/macOS but not Windows because UDS-only behavior was assumed. | Medium — bundle authors get bug reports from Windows users. | Medium — the obvious failure mode of mixing transports. | The transport package automatically substitutes `http_loopback` for `uds` on Windows with a warning; bundle resolver flags transport mismatches at load. Cross-platform CI matrix includes Windows. |
| R4 | LangChain daemon integration shape changes once that mission begins (e.g., the daemon turns out to need a non-A2A control channel). The peer-profile shape no longer fits cleanly. | Medium — schema migration on `a2a_peer` artifact. | Medium. | Treat the peer profile as additive-only across versions; keep `inline_card` and `well_known` paths flexible enough to cover daemon-bootstrap scenarios. ADR will pin the daemon's wire contract once that mission lands. |
| R5 | Audit-event volume: a high-frequency streaming Task can emit hundreds of `message_sent` / `message_received` events, blowing past event-log append throughput. | Medium — performance regression on chatty peers. | Medium-high for streaming use cases. | Coalesce contiguous chunk events into a single `chunk_batch` event keyed by `(task_id, sequence_range)`; unit-test the coalescer; expose a per-peer flag to opt out for cases where every chunk must be individually audited. |
| R6 | A resolved credential leaks into a payload because a provider SDK round-trips a request body through the wire. | Critical — NFR-004 violation. | Medium — `a2a-go` may surface request bodies in error structures. | Audit emitter reconstructs a redacted view from `core/acp` types, never from the SDK's wire body. Tests assert zero plaintext bytes across the full A2A traffic matrix (SC-004). |
| R7 | Cancellation doesn't propagate to the underlying HTTP/2 stream; peer keeps generating after cancel. | High — NFR-002 violation, FR-010 violation, possible cost amplification. | Medium for SDK-wrapped clients. | Envelope MUST honor a `context.Context` deadline AND expose a `Cancel()` that closes the response body. Cancellation test asserts socket close within 1s p99 against a slow-stream fake. |
| R8 | `http_public` transport ships and an operator escalates to it without realizing the lack of signed-card verification. Cross-org peer impersonation becomes possible. | High — security boundary failure. | Low if guardrails hold; high if they don't. | C-006: public exposure with unsigned cards is prohibited at code level — `http_public` transport refuses to dial/listen if `verify.Verifier` is the no-op variant. CI lint asserts the check. |
| R9 | Bundle resolver activation race: an inbound A2A connection arrives before exposed-agent registration completes. Skill router returns "not found" for skills that exist. | Low security; medium UX. | Medium at startup. | Server listener is bound only after bundle resolution completes; pre-bind connections are refused at the transport layer (US2 / edge case). Tests assert ordering. |
| R10 | Policy-engine integration call sites are stubbed too aggressively, so v1 ships with no enforced peer allowlists in the enterprise build. | High enterprise impact. | Low if explicitly tracked. | Even pre-policy-engine, integration call sites in §6.5 ship as `PolicyGuard` interface calls returning `Allow` from a no-op impl; replacing the no-op with the real engine is a one-binding change; CI lint warns when no-op is bound. |

---

## 9. Open Questions for the User

These remain unresolved after research; resolving each materially shapes
implementation. Defaults from the spec are noted; the listed questions
here are the spec's `[NEEDS CLARIFICATION]` items recast as planning
choices.

### Unresolved from the spec

1. **Default local transport on macOS/Linux (spec OQ-1, FR-007)** —
   Unix domain socket or loopback TCP? Plan default (matches spec
   default): **UDS on macOS/Linux**, loopback TCP on Windows; per-bundle
   override available. Rationale: UDS gives stronger isolation
   (filesystem permissions, no port collision) and matches the local-first
   posture better than an open localhost TCP port.

2. **`a2a-go` SDK dependency model (spec OQ-2, FR-015, NFR-010)** —
   direct module dependency or vendored under
   `core/acp/envelope/vendor/`? Plan default: **direct module dependency**,
   pinned to a specific minor version, with a CI gate that flags major-
   version bumps for explicit review. Matches existing SDK posture in the
   LLM connector; the envelope-upgrade ADR can flip to vendoring without
   changing the public API.

3. **Default Agent Card cache TTL (spec OQ-3, FR-008)** — Plan default:
   **300 seconds**, configurable per-peer via `card_cache_ttl_s`. Balances
   fetch overhead against propagation lag.

---

## Charter Check

Per `spec-kitty charter context --action plan`:

- **DIRECTIVE_001**: PASS by construction — only `core/acp/envelope/`
  imports `a2a-go`; CI guard rule blocks cross-package SDK imports
  (§2, R1).
- **DIRECTIVE_003**: PASS — every material trade-off recorded; ADRs will
  cover the envelope-upgrade procedure and the bundle artifact-kind
  contracts.
- **DIRECTIVE_010**: PASS — every FR/NFR/C is cited in the corresponding
  section; no material deviations in v1.
- **DIRECTIVE_024**: PASS — transport split, client/server split, and
  verifier seam keep blast radius local.
- **DIRECTIVE_028**: PASS — black-box tests via the SDK's test-server
  primitives are the default; live-network tests are opt-in.
- **DIRECTIVE_030**: tasks must enforce `go test ./... -race`, `go vet`,
  `golangci-lint`, and ≥ 80% coverage on `core/acp/**`.
- **DIRECTIVE_036**: PASS — tests drive the public `A2AClient` /
  `A2AServer` surface; envelope internals are not asserted directly.

No charter conflicts to escalate.

---

## Phase 0 / Phase 1 artifact status

- **Phase 0 (`research.md`)**: ALREADY GENERATED. Protocol landscape,
  SDK choice (`a2a-go`), and deployment posture all resolved.
- **Phase 1 (`data-model.md`, contracts, quickstart)**: data model in
  `data-model.md`; contracts in §3 of this plan; quickstart promoted
  during the tasks phase.

---

## Branch contract — restated for hand-off

Feature branch: `feat/acp-orchestration-01KQ17ZK`. Merge target: `main`.
All work ships via PR with ≥ 1 maintainer review and squash-merge default.
No direct push to `main`. Suggested next command:
`/spec-kitty.tasks --mission acp-orchestration-01KQ17ZK`.
