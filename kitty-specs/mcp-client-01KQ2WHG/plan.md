# Implementation Plan: MCP Client — Outbound Model Context Protocol Integration

**Mission**: `mcp-client-01KQ2WHG`
**Spec**: `kitty-specs/mcp-client-01KQ2WHG/spec.md`
**Branch contract (from `setup-plan`)**:
- Feature branch: `feat/mcp-client-01KQ2WHG`
- Planning base / merge target: `feat/wire-integration` (per `meta.json`)
- All changes ship via PR; squash-merge default; ≥ 1 maintainer review.

> Branch contract restated: feature work lands on `feat/mcp-client-01KQ2WHG`,
> targets `feat/wire-integration`, and ships via PR. No direct push under any
> circumstance.

---

## 1. Overview

This plan turns the MCP-client spec into a concrete Go architecture under
`core/mcp/client/`. The client is the single in-tree surface that dials
out to MCP servers — local stdio child processes and remote HTTP / SSE /
streamable-HTTP endpoints — declared by bundles, while preserving the
charter's local-first, security-first, configuration-first, and
SOC 2-ready posture.

Bounding scope (v1):

- Day-one transports (FR-001): stdio, HTTP+SSE (legacy compat),
  streamable-HTTP.
- Protocol surface (FR-004 — FR-010): `initialize`/`initialized`,
  `tools/list` & `tools/call`, `prompts/list` & `prompts/get`,
  `resources/list`+`/read`+`/templates/list`, server logging, sampling
  callbacks (server → client), roots negotiation.
- Reliability (FR-011 — FR-013): cancellation propagation < 1 s p99,
  transient/non-transient error classification, per-server retry with
  exponential backoff and jitter.
- Auditability (FR-014 — FR-016): every handshake, request, response,
  notification, and lifecycle transition emitted to the append-only
  event log under `mcp/` namespace, redacted before persistence;
  replay-determinism support.
- Extensibility (FR-017): pluggable transport contract; new transports
  in their own packages.
- Pre-flight (FR-019): every credential reference resolved (or its
  failure reported) before pool open completes.
- Bundle integration: artifact `kind: mcp_server` registered with the
  bundle-format-resolver.

Explicit non-goals for v1 (per spec Assumptions / Open Questions):

- WebSocket transport.
- mTLS / SSO bridge for HTTP transports (enterprise-only future).
- OAuth / DCR for streamable-HTTP.
- Resumability via `Last-Event-ID` (best-effort drop = transient retry).
- Server-side MCP exposure (separate `mcp-server-01KQ2WNV` mission).

---

## 2. Architectural Placement

The client sits at the `core/mcp/client/` package boundary. The Wails app,
the frontend, and any future hosted backend reach it only through
`core/rpc` (Charter DIRECTIVE_001; spec C-001). Transports live in their
own sub-packages so the connection layer is the only seam.

The pre-existing `core/mcp/pool.go` interface (already imported by
`core/core.go`) is preserved; this mission extends it with the new methods
and lands the implementation under `core/mcp/client/`.

```
core/mcp/
├── pool.go                 # Existing public façade — extended in this mission.
├── client/
│   ├── client.go           # Public types: Pool impl, Tool, Prompt, Resource,
│   │                       # ToolCallResult, ContentBlock, ServerSpec extensions,
│   │                       # error taxonomy.
│   ├── connection.go       # Per-server connection state machine; routes JSON-RPC
│   │                       # requests/responses; manages handshake.
│   ├── pool.go             # Pool implementation; lifecycle Open/Close/Reload;
│   │                       # fans out to connections.
│   ├── handlers.go         # Inbound request handlers (sampling, roots) routed
│   │                       # back to harness subsystems.
│   ├── retry.go            # Per-server retry policy + exponential backoff +
│   │                       # full jitter.
│   ├── audit.go            # Event-log adapter — emits mcp/* event kinds with
│   │                       # redaction-aware payload builders.
│   ├── spec_schema.go      # Validation of bundle artifact kind: mcp_server.
│   ├── bundleartifact.go   # ArtifactKindHandler registered with bundle resolver.
│   ├── replay.go           # Replay-mode transport that returns recorded responses
│   │                       # from event log instead of issuing wire calls.
│   ├── jsonrpc/            # Internal JSON-RPC 2.0 framing + canonical method
│   │   │                   # parameter/result struct types. NEVER imported
│   │   │                   # outside core/mcp/client/**.
│   │   ├── frame.go        # Request, Response, Notification, Error.
│   │   ├── methods.go      # initializeParams/Result, listToolsResult, etc.
│   │   └── codes.go        # JSON-RPC error code constants.
│   ├── transport/          # Pluggable transport contract (FR-017).
│   │   └── transport.go    # Transport interface; transport-package registry.
│   ├── stdio/              # stdio transport — uses os/exec + bufio.
│   ├── httpsse/            # HTTP+SSE legacy transport — uses net/http.
│   ├── streamable/         # streamable-HTTP transport — uses net/http.
│   └── internal/           # Shared helpers (id-generator, signal-safe pipe wrap).
└── (future) server/        # mcp-server-01KQ2WNV mission lands here.
```

Architectural-integrity invariants:

- No package outside `core/mcp/client/jsonrpc/` references JSON-RPC frame
  types directly; the public API speaks `core/mcp/client.Tool` etc.
- No package outside `core/mcp/client/<transport>/` imports a
  transport-specific stdlib package in a way that would prevent another
  transport from being added.
- `core/mcp/pool.go` is the only place that defines the public Pool
  contract; transport packages do not expose Pool-shaped types.
- Wails / RPC / UI never import `core/mcp/client/<transport>/` directly —
  they go via `core/mcp` Pool through `core/rpc`.

---

## 3. Public API (Illustrative Signatures)

These signatures extend (do not replace) the existing `core/mcp/pool.go`
stub. Implementation details land in tasks.

```go
package mcp // existing core/mcp package

// ServerSpec extends the existing struct with retry / limits / roots.
type ServerSpec struct {
    Name      string            `json:"name"`
    Transport string            `json:"transport"` // "stdio" | "http_sse" | "streamable_http"
    Command   []string          `json:"command,omitempty"`
    URL       string            `json:"url,omitempty"`
    Env       map[string]string `json:"env,omitempty"`
    Headers   map[string]string `json:"headers,omitempty"` // values may be cred refs
    Roots     []string          `json:"roots,omitempty"`
    Retry     RetryPolicy       `json:"retry,omitempty"`
    Limits    Limits            `json:"limits,omitempty"`
}

type Tool struct {
    Server      string          `json:"server"`
    Name        string          `json:"name"`
    Description string          `json:"description"`
    InputSchema json.RawMessage `json:"input_schema"`
}

type Prompt struct {
    Server      string           `json:"server"`
    Name        string           `json:"name"`
    Description string           `json:"description"`
    Arguments   []PromptArgument `json:"arguments,omitempty"`
}

type Resource struct {
    Server   string `json:"server"`
    URI      string `json:"uri"`
    Name     string `json:"name,omitempty"`
    MIMEType string `json:"mime_type,omitempty"`
}

// Pool extends the existing interface (pre-existing methods preserved).
type Pool interface {
    Open(ctx context.Context, specs []ServerSpec) error
    Close(ctx context.Context) error
    Reload(ctx context.Context, newSpecs []ServerSpec) error  // NEW (FR-014)
    Tools(ctx context.Context) ([]Tool, error)
    Call(ctx context.Context, server, tool string, args json.RawMessage) (json.RawMessage, error)

    // NEW v1 methods
    Prompts(ctx context.Context) ([]Prompt, error)
    GetPrompt(ctx context.Context, server, prompt string, args map[string]string) (PromptResult, error)
    Resources(ctx context.Context) ([]Resource, error)
    ReadResource(ctx context.Context, server, uri string) (ResourceContent, error)
    Health(ctx context.Context) []ServerHealth // per-server status snapshot

    // Transport extensibility (FR-017)
    RegisterTransport(kind string, factory TransportFactory)
}

// Transport is the pluggable per-transport contract (FR-017).
type Transport interface {
    Send(ctx context.Context, frame []byte) error
    Recv(ctx context.Context) ([]byte, error)
    Close() error
}

type TransportFactory func(spec ServerSpec, deps TransportDeps) (Transport, error)

type TransportDeps struct {
    Secrets SecretsResolver  // resolves credential refs in env / headers
    Audit   AuditEmitter
}

// SamplingHandler is invoked when a server issues sampling/createMessage
// (FR-009). Wired by the embedder to route through core/llm.Registry.
type SamplingHandler interface {
    Sample(ctx context.Context, server string, req SamplingRequest) (SamplingResponse, error)
}
```

Errors form a typed taxonomy: `ErrServerUnknown`, `ErrToolUnknown`,
`ErrTransportFailure`, `ErrHandshakeFailed`, `ErrRetryBudgetExhausted`,
`ErrCancelled`, `ErrInvalidParams`, `ErrMethodNotFound`, `ErrServerError`,
`ErrSamplingUnavailable`, `ErrPolicyDenied`, `ErrResultTooLarge`. Adapters
classify; pool acts.

---

## 4. Internal Layering

Tool-call pipeline (left = entry; right = wire):

```
Caller (rpc/session)
  └─→ Pool.Call
        ├─ Server lookup (FR-018)
        ├─ Tool lookup (FR-018)
        ├─ Argument schema validation (FR-005)
        ├─ AuditEmitter.toolCallRequest(...)            (FR-014, FR-015)
        ├─ Connection.send(jsonrpc.tools/call, args)
        │     └─ RetryMiddleware.run(send → adapter)    (FR-013)
        │           └─ Transport.Send / Transport.Recv  (transport-specific)
        ├─ AuditEmitter.toolCallResponse(...)           (FR-014)
        └─ return result | typed error
```

Pool open / reload pipeline:

```
Embedder
  └─→ Pool.Open(specs)
        ├─ Pre-flight: resolve every credential ref     (FR-019)
        ├─ AuditEmitter.poolOpen(...)
        ├─ For each spec, in parallel (capped):
        │     ├─ Spawn / connect transport
        │     ├─ Connection.initialize() — handshake    (FR-004)
        │     ├─ AuditEmitter.serverInitialized(...)
        │     └─ Mark connection ready / unhealthy
        └─ Return aggregate health snapshot
```

Layers in detail:

- **Connection state machine**: each `connection` owns one transport, the
  in-flight-request table (keyed by JSON-RPC id), one read goroutine, one
  write goroutine, and the cancellation context. State transitions:
  Connecting → Initializing → Ready → (Unhealthy on retry-exhaust) →
  Closing → Closed.

- **JSON-RPC layer** (`core/mcp/client/jsonrpc/`): pure framing. Knows
  nothing about transports; emits and parses opaque byte frames with
  request/response/notification semantics. Owns the JSON-RPC `id`
  generator and the response routing table contract.

- **Transport layer**: each transport package implements the `Transport`
  interface. `stdio` uses `os/exec.Cmd` + `bufio.Scanner` with a 16 MiB
  ceiling. `httpsse` uses `net/http` with two connections per session.
  `streamable` uses `net/http` with content-type detection on response.

- **RetryMiddleware** (FR-013): on transport-level send failure or
  `notifications/cancelled` from the server, classify as transient or
  non-transient. Transient: exponential backoff with full jitter, bounded
  by per-server `RetryPolicy`, emit `mcp/retry_attempted`. Non-transient
  (e.g., `MethodNotFound`): bypass retry. Streaming-aware: if any chunks
  have already been delivered to the caller, treat further drops as
  terminal (no double-bill).

- **AuditEmitter** (FR-014, FR-015): single chokepoint to event-log.
  Every payload passes through redaction (`core/event` redaction pipeline)
  before persistence. The client itself never logs resolved credential
  bytes; the redaction layer is defense-in-depth, not the primary
  guarantee.

- **PreflightCoordinator** (FR-019): on `Open(specs)`, validate that
  every credential reference (env vars in stdio, header values in HTTP)
  resolves successfully. Failures surface as `mcp/preflight_failed`
  events keyed by server id and never trigger a transport spawn.

- **InboundRequestRouter**: server-initiated requests
  (`sampling/createMessage`, `roots/list`) come in on the read goroutine.
  The router dispatches to embedder-supplied handlers (`SamplingHandler`,
  `RootsProvider`). Default handlers return typed errors when not wired.

- **ReplayMode**: when constructed with `replay: true`, the connection's
  Send/Recv is swapped for a fixture transport that returns recorded
  JSON-RPC responses from the event log. Pool open does not spawn child
  processes or open sockets in replay mode.

---

## 5. Data Model

See `data-model.md` for full type-by-type detail. Summary:

### 5.1 Bundle artifact: `kind: mcp_server`

Stored as YAML inside the bundle. Validated by `spec_schema.go`. Registered
with the bundle resolver via `ArtifactKindHandler`. See `data-model.md §1`.

### 5.2 Credential references

Headers values and stdio env values may be either string literals (rejected
if they match a known credential pattern — defense-in-depth) or
`core/secrets.Reference` shapes resolved at preflight / call time.

### 5.3 Event-log kinds

Namespaced `mcp/`. Full list in `data-model.md §4`. Per US3 Acceptance 1, a
successful tool call produces at minimum `pool_open` →
`server_initialize` → `server_initialized` → `tool_call_request` →
`tool_call_response` → `pool_close`.

### 5.4 JSON-RPC method coverage

The `jsonrpc/methods.go` types cover, at minimum:

- `initialize` / `initialized`
- `tools/list`, `tools/call`
- `prompts/list`, `prompts/get`
- `resources/list`, `resources/read`, `resources/templates/list`
- `logging/setLevel`, `notifications/message`
- `sampling/createMessage` (server → client request)
- `roots/list`, `notifications/roots/list_changed`
- `notifications/cancelled`, `notifications/progress`, `ping`

---

## 6. Integration Points

### 6.1 secrets-keychain-01KQ1A3M

- The client calls `core/secrets.Backend.Resolve(ref)` at preflight and
  request time for every credential reference (header values, stdio env
  values).
- Resolved bytes live in a `core/secrets.Secret` (`[]byte`-typed; never
  `string`) and are zeroized after the transport spawns / sends the
  request.
- Pre-flight (FR-019) calls `core/secrets.PreflightAll` for every loaded
  spec — failures map to `mcp/preflight_failed` events.
- TTL cache: the client relies on the upstream cache; no parallel
  credential cache.

### 6.2 event-log-01KQ1A3M

- All emit goes through `core/event.Log.Append`.
- Event kinds registered under the `mcp/` namespace.
- Redaction is the event-log pipeline's responsibility; the client's
  contract is "never put resolved credentials into the payload in the
  first place" (defense-in-depth alignment).
- Replay determinism: each `mcp/tool_call_request` payload includes the
  bundle's `ResolvedGraph.snapshot_id` so replay can recreate exact
  routing state.

### 6.3 bundle-format-resolver-01KQ1A3J

- Client registers an `ArtifactKindHandler` for `kind: mcp_server` at
  process start.
- Handler signature follows the upstream contract:
  `Parse(bytes) → ServerSpec`, `Validate(ServerSpec, ManifestCtx) →
  errors`, `Activate(ServerSpec, ResolverCtx) → registration with the
  Pool's pending spec list at next Reload`.
- Activation order is deterministic per `ResolvedGraph.activation_order`.
  Server-id collisions surfaced through resolver `FR-009` conflict-detection.

### 6.4 llm-connector-01KQ1770 (sampling callbacks)

- The client's `SamplingHandler` interface is wired by the embedder
  (`core.Core.Start`) to a function that calls `core/llm.Registry.Stream`
  under the bundle's resolved provider profile.
- Sampling returns a `Stream` from the LLM connector; the client
  collects the final response and wraps it into MCP's
  `sampling/createMessage` response shape.
- Policy gate (charter policy-engine integration): the embedder may inject
  a `PolicyGuard` that inspects the sampling request before routing. v1
  ships with a no-op guard.

### 6.5 policy-engine-01KQ1A3N

The MCP client is one of the policy engine's most-constrained consumers
once that mission lands. Integration touches three points (no-op stubs
ship with this mission):

- **Server registration** — `LLM.Allowlist(server)` style check at
  `Pool.Open` time. Disallowed servers emit `mcp/policy_denied` and are
  not registered.
- **Per-call gate** — before retry middleware, `PolicyGuard.allow(req)`
  enforces tool-name allowlists, argument-shape constraints, and
  per-bundle quota.
- **Sampling gate** — sampling callbacks inspected as in §6.4; deny
  surfaces as `ErrPolicyDenied`.

### 6.6 core/rpc

The Wails frontend reaches the Pool exclusively via `core/rpc` (charter:
same RPC surface a future hosted backend would expose; spec C-001). RPC
methods envisioned (defined in tasks, not here):

- `MCP.ListServers() → [ServerHealth]`
- `MCP.ListTools() → [Tool]`
- `MCP.CallTool(req) → ToolCallResult`
- `MCP.ListPrompts() → [Prompt]`
- `MCP.GetPrompt(req) → PromptResult`
- `MCP.ListResources() → [Resource]`
- `MCP.ReadResource(uri) → ResourceContent`

---

## 7. Phasing

### v1.0 — this mission (all three day-one transports)

Scope:

- JSON-RPC 2.0 framing layer + canonical method types.
- Transport contract + three transports (stdio, http_sse, streamable).
- Pool implementation with Open/Close/Reload.
- Connection state machine with handshake + protocol-version negotiation.
- All FR-005 / FR-006 / FR-007 / FR-008 / FR-009 / FR-010 protocol surface.
- Cancellation propagation < 1 s p99.
- Retry middleware + transient/non-transient classification.
- Pre-flight credential resolution + actionable startup errors.
- Audit emit for every event kind in §5.3.
- Bundle artifact-kind handler for `kind: mcp_server`.
- Replay-mode transport.
- Test coverage: ≥ 80 % `core/mcp/client/**` line; black-box tests
  against fixture servers (a tiny test stdio binary in `testdata/`,
  recorded HTTP fixtures).

### v1.x — fast-follows (separate missions)

- WebSocket transport.
- Resumability via `Last-Event-ID` for streamable-HTTP.
- OAuth / DCR for streamable-HTTP.
- Bundle artifact `kind: mcp_tool` (bundle-contributed inbound tools).
- mTLS / SSO bridge (enterprise-only, build-tagged).

### v2 — out of scope this spec

- MCP server-side surface (separate `mcp-server-01KQ2WNV` mission).
- Cross-pool orchestration (multi-bundle pool federation).

---

## 8. Risk Register

Premortem-driven (Charter Tactic `premortem-risk-identification`). Top
failure modes and mitigations:

| # | Risk | Impact | Likelihood | Mitigation |
|---|---|---|---|---|
| R1 | A transport package leaks types into the Pool contract (e.g., a `*http.Response` reaches the Pool API), violating DIRECTIVE_001 / FR-017. | High — collapses the extensibility story; every future transport becomes a core change. | Medium without guardrails. | CI lint: any non-test file outside `core/mcp/client/<transport>/` that imports `os/exec` / SSE-specific types fails the build. Pool API speaks only `core/mcp` types. |
| R2 | A resolved credential leaks into a JSON-RPC payload via header round-trip. | Critical — NFR-007 violation, breaks SOC 2 posture. | Medium — HTTP transports build headers from resolved cred refs. | Audit emitter reconstructs the redacted view from the typed `ServerSpec` shape, NOT from the wire frame. Transports MUST NOT log raw HTTP request bodies. Tests assert zero plaintext credential bytes across the full transport matrix (SC-003). |
| R3 | A stdio child process buffers stdout in 64 KB chunks; large tool results arrive partial and parsing fails. | High — tool calls fail mysteriously on large payloads. | High without explicit sizing. | `bufio.Scanner` with `Buffer(make([]byte, 0, 16<<20))` and `MaxScanTokenSize(16<<20)`. Test with fixture servers that return 2 / 8 / 16 MiB results. |
| R4 | A streaming retry double-sends a tool call. Adapter declares "transient" mid-stream; middleware retries; both attempts produce side effects. | High — operator surprise; some tools mutate external state. | Low if explicitly designed; otherwise high. | Streaming retry only re-issues if **zero** chunks have been delivered. Once a chunk is forwarded, mid-stream drops are surfaced as terminal `error` events; the upstream session retry is the operator's choice, not the pool's. |
| R5 | Cancellation doesn't propagate to the underlying HTTP/2 stream / stdio pipe; the server keeps generating. | High — NFR-002 violation, FR-011 violation. | Medium for SDK-wrapped clients that own connection lifecycle. | Transports MUST honor `context.Context` deadline AND expose `Close()` that closes the underlying body / pipe within 1 s p99. Cancellation tests: assert socket / pipe close within 1 s p99 against a slow-stream fake. |
| R6 | A bundle reload tears down a server with in-flight tool calls; the agent sees its results vanish. | Medium — usability. | Medium. | Reload algorithm preserves unchanged servers (US5 Acceptance 1). Removed servers receive a graceful drain (max drain timeout, default 5 s). In-flight calls against a draining server complete OR receive `ErrCancelled`, never simply lost. |
| R7 | A buggy MCP server crashes repeatedly; the client respawns it forever, eating CPU. | Medium — local DoS. | Medium. | Per-server retry budget caps respawn count; once exhausted, server marked unhealthy and removed from active pool until the next bundle reload (US4 Acceptance 4). |
| R8 | An HTTP MCP server serves a `Content-Type: text/event-stream` body but uses a non-standard frame format. | Low — limited blast radius. | Low. | Strictly conformant SSE parser (RFC 8895 alignment, `data:`-prefixed lines + blank line terminator). Non-conformant frames emit `mcp/protocol_warning` and the request fails as `ErrInvalidParams`. |
| R9 | Event-log redaction recall < 99 % on novel credential shapes returned in tool results. | Medium — NFR-007 boundary. | Medium. | Co-evolve pattern catalog with the event-log mission; the MCP client contributes patterns from common tool ecosystems (filesystem credentials, AWS arn shapes, etc.). |
| R10 | Sampling callback infinite-loops when the LLM connector itself uses an MCP server (cycle). | High — runaway cost / latency. | Low. | Sampling depth counter on each `Pool.Call`; default max depth 4; exceeding emits `ErrSamplingDepthExceeded` and the chain unwinds. |
| R11 | `os/exec` zombies on macOS when stdio servers crash unexpectedly; resource leak across long-running sessions. | Medium — OS-level. | Medium. | Reaper goroutine per spawned child; `cmd.Wait()` always called; cleanup verified by integration test that spawns + crashes 1000 children. |

---

## 9. Open Questions for the User

These remain unresolved after research; resolving each materially shapes
implementation. Defaults from the spec are noted; the listed questions are
the ones the user explicitly flagged as `[NEEDS CLARIFICATION]` plus a
small handful surfaced during planning.

### Unresolved from the spec

1. **Sampling fan-out policy (spec OQ-1)** — when a sampling callback
   arrives, do we honor the server's `model` hint or always route through
   the bundle's default profile? Plan default (matches spec default):
   **prefer the bundle's default; allow hint only when the policy engine
   permits arbitrary model selection**.

2. **Roots scope source (spec OQ-2)** — derive roots from the bundle's
   `paths` field, from `dataDir`, or from explicit per-server `roots:`
   configuration? Plan default (matches spec default): **explicit
   per-server `roots:` only**.

3. **Transport fallback (spec OQ-3)** — silently downgrade
   `streamable_http` to `http_sse` on handshake failure? Plan default
   (matches spec default): **no — explicit transport kind; fallback is
   the operator's choice expressed by declaring two MCP servers**.

### Surfaced during planning

4. **Inbound `roots/list` response source** — when a server requests the
   client's roots, do we (a) return only the bundle-declared roots, (b)
   also include the harness's data directory, or (c) include the
   bundle-declared roots plus the bundle's own bundle-mount path? Plan
   default if unresolved: **(a) bundle-declared roots only**, never
   `dataDir`.

5. **Concurrent pool open parallelism cap** — `Pool.Open` for N servers
   spawns / connects in parallel. Cap at 16, 32, or unbounded? Plan
   default if unresolved: **16**, configurable via top-level config.

6. **Stdio process kill grace period** — on Pool close, send SIGTERM,
   wait, then SIGKILL after how long? Plan default if unresolved: **5 s
   SIGTERM grace, then SIGKILL**.

7. **Replay-mode behavior on missing recordings** — if replay finds a
   tool call with no recorded response, fail fast or fall back to live
   execution? Plan default if unresolved: **fail fast with
   `ErrReplayMissingRecording`** — fallback would silently break replay
   determinism.

---

## Charter Check

Per `spec-kitty charter context --action plan` (loaded above):

- **DIRECTIVE_001 (Architectural Integrity)**: PASS by construction —
  every transport lives in its own sub-package; the connection layer
  speaks only `core/mcp` types; CI guard rule blocks cross-package
  transport-specific imports (§2, R1).
- **DIRECTIVE_003 (Decision Documentation)**: PASS — every material
  trade-off (hand-rolled vs SDK, transport plurality, replay design,
  sampling routing) is recorded in this plan; an ADR will accompany the
  bundle artifact-kind contract for `mcp_server` (one ADR, drafted
  during tasks).
- **DIRECTIVE_010 (Specification Fidelity)**: PASS — every FR/NFR/C is
  cited in the corresponding section. Deviations (none material in v1)
  would be called out in §9 or in the ADR.
- **DIRECTIVE_024 (Locality of Change)**: PASS — transport-by-transport
  decomposition keeps blast radius inside each transport package.
- **DIRECTIVE_028 (Efficient Local Tooling)**: PASS — black-box tests
  via recorded fixture servers and HTTP fixtures; live-network tests
  opt-in behind a flag.
- **DIRECTIVE_029 (Agent Commit Signing)**: applies at implementation
  time, not planning.
- **DIRECTIVE_030 (Test and Typecheck Quality Gate)**: tasks must
  enforce `go test ./... -race`, `go vet`, `golangci-lint`, and ≥ 80 %
  coverage on `core/mcp/client/**`.
- **DIRECTIVE_033 (Explicit Staging)**: applies at commit time.
- **DIRECTIVE_036 (Black-Box Testing)**: PASS — pool tests drive the
  Pool through its public API; transport-internal helpers are not
  asserted directly.

No charter conflicts to escalate.

---

## Phase 0 / Phase 1 artifact status

- **Phase 0 (`research.md`)**: GENERATED — see `research.md` plus
  `research/evidence-log.csv` and `research/source-register.csv`.
- **Phase 1 (`data-model.md`)**: GENERATED — see `data-model.md`. The
  contracts live in §3 of this plan.

---

## Branch contract — restated for hand-off

Feature branch: `feat/mcp-client-01KQ2WHG`. Planning base / merge target:
`feat/wire-integration`. All work ships via PR with ≥ 1 maintainer review
and squash-merge default. Suggested next command for the user:
`/spec-kitty.tasks --mission mcp-client-01KQ2WHG`.
