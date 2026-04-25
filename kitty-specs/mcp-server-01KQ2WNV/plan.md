# Implementation Plan: MCP Server — Inbound Model Context Protocol Surface

**Mission**: `mcp-server-01KQ2WNV`
**Spec**: `kitty-specs/mcp-server-01KQ2WNV/spec.md`
**Branch contract (from `setup-plan`)**:
- Feature branch: `feat/mcp-server-01KQ2WNV`
- Planning base / merge target: `feat/wire-integration` (per `meta.json`)
- All changes ship via PR; squash-merge default; ≥ 1 maintainer review.

> Branch contract restated: feature work lands on `feat/mcp-server-01KQ2WNV`,
> targets `feat/wire-integration`, and ships via PR. No direct push under any
> circumstance.

---

## 1. Overview

This plan turns the MCP-server spec into a concrete Go architecture under
`core/mcp/server/`. The server is the in-tree surface that exposes the
harness as an MCP server to external clients (Claude Desktop, Cursor,
custom MCP-aware agents) — over stdio (the harness as a child process)
and streamable-HTTP (an opt-in network listener).

Bounding scope (v1):

- Day-one transports (FR-001): stdio and streamable-HTTP. HTTP+SSE NOT
  exposed inbound (the older transport remains client-side for outbound
  compatibility).
- Protocol surface (FR-003 — FR-009): handshake with capability +
  protocol-version negotiation, `tools/list`/`tools/call`,
  `prompts/list`/`prompts/get`, `resources/list`+`/read`+`/templates/list`,
  server logging notifications, sampling callbacks, roots negotiation.
- Reliability (FR-010 — FR-011): cancellation propagation < 1 s p99,
  transient/non-transient error classification.
- Auditability (FR-014 — FR-015): every handshake, request, response,
  notification, and lifecycle transition emitted to the append-only
  event log under `mcp.server/` namespace, redacted before persistence.
- Security (FR-016 — FR-017): origin / allowlist policy on
  streamable-HTTP, optional bearer-token auth referencing a credential
  via `core/secrets`.
- Tool surface (FR-018): five harness-native tools day-one:
  `run_bundle`, `list_bundles`, `query_event_log`, `list_sessions`,
  `list_provider_profiles`.
- Lifecycle integration (FR-019, FR-020): `Start`/`Shutdown` integrate
  with `core.Core.Start`/`Shutdown`; graceful drain.
- Extensibility (FR-012, FR-013): pluggable transport contract; pluggable
  tool contract.

Explicit non-goals for v1 (per spec Assumptions / Open Questions):

- Inbound HTTP+SSE transport.
- WebSocket transport.
- mTLS / SSO bridge for HTTP transports (enterprise; deferred per C-005).
- OAuth / DCR.
- Bundle-contributed tools via `kind: mcp_tool` (deferred per spec OQ-1;
  design accommodates).
- Server-pushed `notifications/tools/list_changed` on bundle reload (the
  spec accommodates; v1 omits — clients can re-list on next handshake).

---

## 2. Architectural Placement

The server sits at the `core/mcp/server/` package boundary. The Wails app,
the frontend, and any future hosted backend reach it only by virtue of
the harness running as a process — the server is a peer of the existing
RPC surface, not a consumer of it (Charter DIRECTIVE_001; spec C-001).
Transports live in their own sub-packages so the dispatch layer is the
only seam.

```
core/mcp/
├── pool.go                 # Existing — owned by mcp-client mission.
├── client/                 # Owned by mcp-client mission.
│   └── jsonrpc/            # JSON-RPC framing layer — REUSED by server.
│                           # During this mission's WP02, optionally
│                           # refactored to core/mcp/jsonrpc/ for symmetry.
└── server/
    ├── server.go           # Public façade: Server interface, Start/Shutdown,
    │                       # Tool / Session types, error taxonomy.
    ├── session.go          # Per-session state machine; routes JSON-RPC
    │                       # frames; manages handshake.
    ├── dispatcher.go       # Method-name → handler dispatch table.
    ├── handlers.go         # Method handlers: tools/list, tools/call,
    │                       # prompts/*, resources/*, etc.
    ├── tools.go            # Tool registration + Catalog.
    ├── tools/              # Built-in tool implementations:
    │   ├── runbundle.go
    │   ├── listbundles.go
    │   ├── queryeventlog.go
    │   ├── listsessions.go
    │   └── listproviderprofiles.go
    ├── prompts.go          # prompts/list and prompts/get handlers backed
    │                       # by the bundle resolver.
    ├── resources.go        # resources/* handlers backed by the bundle
    │                       # resolver.
    ├── roots.go            # roots/list source from operator-declared paths.
    ├── sampling.go         # Outbound sampling/createMessage handler;
    │                       # routes through core/llm.Registry.
    ├── audit.go            # Event-log adapter — emits mcp.server/* event
    │                       # kinds with redaction-aware payload builders.
    ├── config.go           # Configuration shape + validation.
    ├── config_schema.go    # validation for mcp.server.* config block.
    ├── transport/          # Pluggable transport contract.
    │   └── transport.go
    ├── stdio/              # stdio transport (stdin/stdout framing).
    └── streamable/         # streamable-HTTP transport (net/http server).
```

Architectural-integrity invariants:

- No package outside `core/mcp/server/<transport>/` imports a
  transport-specific stdlib package (`net/http` server side,
  `os/exec`-style adoption for stdio is on the CLI side).
- Wails / RPC / UI never expose MCP server APIs directly — the server
  is its own listener.
- `core/mcp/server/` may import in-tree packages (`core/llm`,
  `core/event`, `core/secrets`, `core/bundle`, `core/session`,
  `core/scheduler`); it does NOT import Wails or frontend types.

---

## 3. Public API (Illustrative Signatures)

These signatures live in `core/mcp/server/server.go`.

```go
package server

// Server is the inbound MCP server façade.
type Server interface {
    Start(ctx context.Context) error
    Shutdown(ctx context.Context) error
    Sessions() []SessionSnapshot
    RegisterTransport(kind string, factory TransportFactory)
    RegisterTool(t Tool)
}

// Options carries the bootstrap configuration. Wired by core.Core at start.
type Options struct {
    Config    Config
    LLM       llm.Registry
    Event     event.Log
    Secrets   secrets.Backend
    Bundle    bundle.Resolver
    Session   session.Executor
    Scheduler scheduler.Scheduler
    Policy    PolicyGuard // optional; nil → no-op
}

// Tool is the harness-native tool contract (FR-013, FR-018).
type Tool interface {
    Name() string
    Description() string
    InputSchema() json.RawMessage
    Call(ctx context.Context, args json.RawMessage, sess Session) (ToolResult, error)
}

// Session is the per-client session handle exposed to tool handlers.
type Session interface {
    ID() string
    ClientInfo() ClientInfo
    Sample(ctx context.Context, req SamplingRequest) (SamplingResponse, error)
    NotifyProgress(token string, p Progress)
    Logf(level LogLevel, format string, args ...any)
}

// PolicyGuard inspects sampling and tool-call requests before dispatch.
type PolicyGuard interface {
    AllowToolCall(ctx context.Context, sess Session, tool, args json.RawMessage) error
    AllowSampling(ctx context.Context, sess Session, req SamplingRequest) error
}

// Transport is the pluggable per-transport contract (FR-012).
type Transport interface {
    Listen(ctx context.Context, accept func(SessionTransport)) error
    Shutdown(ctx context.Context) error
}

// SessionTransport is one open client session's I/O.
type SessionTransport interface {
    Send(ctx context.Context, frame []byte) error
    Recv(ctx context.Context) ([]byte, error)
    Close() error
    Source() string
}

type TransportFactory func(opts TransportOpts) (Transport, error)
```

Errors form a typed taxonomy: `ErrInvalidParams`, `ErrMethodNotFound`,
`ErrInternalError`, `ErrPolicyDenied`, `ErrSamplingUnavailable`,
`ErrAuthDenied`, `ErrOriginDenied`, `ErrResourceTooLarge`,
`ErrSessionClosed`, `ErrShutdown`. Mapped onto JSON-RPC error codes per
plan §4.

---

## 4. Internal Layering

Inbound request pipeline (left = wire entry; right = harness subsystem):

```
External MCP client
  └─→ Transport.Listen (stdio | streamable_http)
        └─→ accept(SessionTransport)
              └─→ session.run(ctx)
                    ├─ session.handshake() — initialize / initialized   (FR-003)
                    ├─ for each inbound frame:
                    │     ├─ AuditEmitter.requestReceived(...)           (FR-014)
                    │     ├─ Dispatcher.lookup(method)
                    │     ├─ PolicyGuard.allow*(...) — gate on samping/tool (C-007)
                    │     ├─ Handler.Handle(ctx, sess, params)
                    │     │     └─ (tool / prompt / resource / sampling / roots logic)
                    │     ├─ AuditEmitter.responseProduced(...)
                    │     └─ SessionTransport.Send(response)
                    └─ on close: AuditEmitter.sessionClosed(...)
```

Layers in detail:

- **Session state machine**: each `session` owns one `SessionTransport`,
  the in-flight handler context map (for cancellation), one read
  goroutine, one write goroutine, and the per-session cancellation
  context. State transitions: Connecting → Initializing → Ready →
  Closing → Closed. Forward-compat: unknown methods return
  `MethodNotFound` rather than abort the session (per MCP forward-compat).

- **JSON-RPC layer**: reused from `core/mcp/client/jsonrpc/` (or
  `core/mcp/jsonrpc/` after WP02 refactor). Pure framing.

- **Transport layer**: `stdio` uses `os.Stdin`/`os.Stdout` with bufio
  framing (16 MiB buffer). `streamable` uses `http.Server` listening on
  a configurable bind, dispatching on `POST /` to JSON-RPC.

- **Dispatcher**: maps method name → `Handler`. Built once at
  `Server.Start`. Includes built-in handlers for every protocol method
  the server supports (§3.1 of research).

- **AuditEmitter**: single chokepoint to event-log. Every payload passes
  through redaction (`core/event` redaction pipeline) before persistence.
  The server itself never logs resolved credential bytes; the redaction
  layer is defense-in-depth.

- **PolicyGuard**: optional. v1 ships with a no-op `nil`-compatible
  guard. The `policy-engine` mission lands the real one. Policy gates
  applied: `AllowToolCall` before `tools/call` dispatch;
  `AllowSampling` before `sampling/createMessage` outbound issue.

- **Handlers**:
  - `tools/list` → enumerate `Catalog.Tools()` filtered by config-enabled
    flags.
  - `tools/call` → look up tool, validate args against schema, gate via
    PolicyGuard, invoke `Tool.Call(ctx, args, sess)`.
  - `prompts/list` → query bundle resolver for prompts; filter `private:
    true`.
  - `prompts/get` → render the prompt via bundle resolver, return
    messages.
  - `resources/list` / `resources/read` → query bundle resolver.
  - `roots/list` (server initiates this; not handled inbound — the
    SERVER issues this when it needs to know the client's roots, NOT
    the other way around. The harness's own roots are exposed via a
    server-side capability advertised at handshake; explicit retrieval
    by the client is a separate flow under `roots/list` advertisement
    in capabilities).

  Wait — re-checking: in the MCP spec, `roots/list` is a CLIENT-side
  feature: the SERVER calls `roots/list` to ask the CLIENT for its
  roots. From this mission's perspective the harness is the server, so
  it would call `roots/list` outbound on its session. However, FR-007
  explicitly talks about exposing the HARNESS's own roots to the client.
  This is a slight asymmetry. Resolution: the server advertises its
  ROOTS capability and exposes operator-declared roots via an
  initialize-capability field; clients read it from the handshake.

  This nuance is captured in WP05 (handlers).

- **Handshake-time gates**:
  - Stdio: timeout for missing `initialize` (default 30 s); refuse to
    start if stdout looks unsafe (TTY with colorization).
  - HTTP: origin / allowlist check before accepting; bearer-token check
    if configured.

- **Graceful shutdown** (FR-020): `Server.Shutdown(ctx)` — stop
  accepting new connections, wait up to `drain_timeout_ms` (default
  30 s) for in-flight handlers to complete, then hard-close.

---

## 5. Data Model

See `data-model.md` for full type-by-type detail. Summary:

### 5.1 Top-level configuration `mcp.server.*`

Stored in the harness config file. Validated by `config_schema.go`.
See `data-model.md §1`.

### 5.2 Authentication credentials

`mcp.server.http.auth.bearer_token` is a credential reference resolved
via `core/secrets`. Inline plaintext is rejected at config validation.

### 5.3 Event-log kinds

Namespaced `mcp.server/`. Full list in `data-model.md §4`. Per US4
Acceptance 1, a successful tool call produces at minimum
`listener_started` → `session_opened` → `handshake` → `tool_call` →
`tool_result` → `session_closed` → `listener_stopped`.

### 5.4 Tool catalog (FR-018)

Five harness-native tools day-one. Each ships its argument schema and
maps onto an existing `core/` subsystem call. Full schemas in
`data-model.md §5`.

### 5.5 Roots set (FR-007)

Operator-declared paths under `mcp.server.roots:`. NEVER `dataDir`,
NEVER `$HOME`, NEVER `/`. Validated at server start.

---

## 6. Integration Points

### 6.1 secrets-keychain-01KQ1A3M

- The server calls `core/secrets.Backend.Resolve(ref)` at start time to
  resolve the bearer-token reference (if configured); cached for the
  listener's lifetime.
- Resolved bytes live in a `core/secrets.Secret` (`[]byte`-typed) and
  are zeroized at shutdown.
- No per-request credential resolution; the bearer token is a startup
  resolution.

### 6.2 event-log-01KQ1A3M

- All emit goes through `core/event.Log.Append`.
- Event kinds registered under the `mcp.server/` namespace.
- Redaction is the event-log pipeline's responsibility; the server's
  contract is "never put resolved credentials into the payload in the
  first place" (defense-in-depth alignment).
- Replay-aware: each `mcp.server/tool_call` entry includes session id
  and tool args (redacted of credential patterns) so an event-log
  replay can audit-reconstruct the call without re-executing.

### 6.3 bundle-format-resolver-01KQ1A3J

- Server holds a `bundle.Resolver` reference, queried at request time
  for prompts and resources.
- Bundle reload semantics: the server does NOT cache prompts or
  resources; every `prompts/list` / `resources/list` call hits the
  resolver. In-flight `prompts/get` / `resources/read` calls finish
  against the resolved-at-call snapshot.
- Future bundle artifact `kind: mcp_tool` (spec OQ-1) will register
  bundle-contributed tools; v1 ships an empty handler for this kind.

### 6.4 llm-connector-01KQ1770 (sampling callbacks)

- The server's `Session.Sample` method routes through `core/llm.Registry.Stream`
  under the bundle's resolved provider profile.
- Sampling profile selection (spec OQ-3 default): the bundle's
  `default_provider` field; if absent, the first registered provider
  profile.
- Policy gate: `PolicyGuard.AllowSampling` inspects the request before
  routing. v1 ships with a no-op guard.
- Sampling-depth counter (default 4) prevents runaway loops.

> **Sampling architecture note**: The MCP spec's canonical pattern is
> "server requests sampling from client; client owns the LLM." The
> harness model in FR-009 is "server owns the LLM via its own
> connector." The server-side handler MAY be reconfigured to round-trip
> the external client's LLM via `sampling/createMessage` outbound; v1
> defaults to internal-LLM sampling (matches FR-009 + spec OQ-3).
> Operators wanting external-LLM sampling can express it with a
> configuration flag (deferred to v1.x).

### 6.5 policy-engine-01KQ1A3N

The MCP server is one of the policy engine's most-constrained consumers
once that mission lands. Integration touches three points (no-op stubs
ship with this mission):

- **Tool call gate** — `AllowToolCall(sess, tool, args)` runs before
  every `tools/call` dispatch. Disallowed calls emit
  `mcp.server/policy_denied` and return `ErrPolicyDenied`.
- **Sampling gate** — `AllowSampling(sess, req)` runs before every
  outbound sampling call. Disallowed sampling emits
  `mcp.server/policy_denied`.
- **Resource access** — future: gate on resource URI scoping.

### 6.6 core/session

- The `run_bundle` tool implementation calls
  `core/session.Executor.Start` to launch a session against the named
  bundle.
- Session-id naming: returned by the executor; the tool result
  includes it as a text content block.

### 6.7 core/scheduler

Not consumed in v1 catalog; reserved for future tools (`schedule_run`,
`list_scheduled_runs`).

### 6.8 CLI subcommand

A new CLI subcommand `kaneaz-harness mcp serve --stdio` is added during
this mission's WP15. The subcommand:

- Parses CLI flags (`--stdio`, `--http`, `--bind`, `--log-file`).
- Constructs a `core.Core` with the MCP server enabled.
- Routes log output to stderr or `--log-file` (NEVER stdout in stdio
  mode).
- Invokes `Server.Start(ctx)` and blocks on shutdown signal.

---

## 7. Phasing

### v1.0 — this mission (stdio + streamable-HTTP)

Scope:

- JSON-RPC framing layer reuse (or refactor).
- Transport contract + two transports (stdio, streamable_http).
- Server lifecycle (`Start`/`Shutdown`) + session state machine.
- Method handlers for: `initialize`, `tools/list`, `tools/call`,
  `prompts/list`, `prompts/get`, `resources/list`, `resources/read`,
  `resources/templates/list`, `logging/setLevel`,
  `notifications/cancelled`, `ping`.
- Five harness-native tools: `run_bundle`, `list_bundles`,
  `query_event_log`, `list_sessions`, `list_provider_profiles`.
- Sampling routing through `core/llm`.
- Roots advertisement from operator config.
- Origin / allowlist + bearer-token auth on streamable-HTTP.
- Audit emit for every event kind in §5.3.
- CLI subcommand `mcp serve --stdio`.
- Test coverage: ≥ 80 % `core/mcp/server/**` line; black-box tests
  against fixture clients + against the actual MCP conformance shape
  (a recorded session).

### v1.x — fast-follows (separate missions)

- Inbound HTTP+SSE transport (legacy compat).
- Bundle artifact `kind: mcp_tool`.
- mTLS / SSO bridge (enterprise-only, build-tagged).
- OAuth / DCR for streamable-HTTP.
- WebSocket transport.
- Server-pushed `notifications/tools/list_changed` on bundle reload.

### v2 — out of scope this spec

- Multi-tenant authentication beyond bearer (per-user OAuth, etc.).
- Federated MCP server clusters.

---

## 8. Risk Register

Premortem-driven (Charter Tactic `premortem-risk-identification`). Top
failure modes and mitigations:

| # | Risk | Impact | Likelihood | Mitigation |
|---|---|---|---|---|
| R1 | A stdio mode launch corrupts stdout because internal logging escapes the harness boundary. | Critical — protocol breaks immediately. | Medium without a startup gate. | The `mcp serve --stdio` CLI subcommand verifies stdout is not a TTY, stops any third-party libraries that auto-write to stdout, and routes all logging to stderr or a file. Test: launch with stdout pointed to a fake colorizing writer; assert refusal to start. |
| R2 | Streamable-HTTP listener bound to a non-loopback interface inadvertently exposes the harness to a hostile LAN. | Critical — privilege escalation surface. | Low if defaults are right; high if defaults are wrong. | Default bind `127.0.0.1:0`. A non-loopback bind requires an explicit second flag (`mcp.server.http.allow_non_loopback: true`). Test: assert bind validation rejects non-loopback without the flag. |
| R3 | Bearer-token comparison is not constant-time → timing oracle. | Medium — auth bypass under cryptographic attack. | Low under loopback default, higher if bound non-loopback. | `subtle.ConstantTimeCompare`. Tests assert constant-time behavior holds. |
| R4 | A tool's result echoes a credential pattern back via the LLM connector or the bundle's data, ending up in the event log. | Medium — NFR-006 boundary. | Medium. | Defense-in-depth: tool implementations MUST NOT include credential bytes in results. Event-log redaction is the second line. Tests assert zero plaintext patterns across the catalog. |
| R5 | A long-running tool call (`run_bundle`) blocks the session goroutine and starves other handlers. | Medium — usability. | Medium. | Each handler runs in its own goroutine with a per-handler context; the session goroutine only routes frames. Test: invoke a slow tool; assert other tools dispatch concurrently on the same session. |
| R6 | Cancellation does not propagate to the tool implementation; client cancels but `run_bundle` keeps mutating. | High — FR-010 violation, wasted work. | Medium. | Tool handlers MUST honor `ctx.Done()`. Test: cancel a slow tool mid-call; assert the handler's context is cancelled within 1 s. |
| R7 | A `tools/call` with malicious / unbounded args runs unbounded resource consumption (e.g., `query_event_log` with no limit). | Medium — local DoS. | Medium. | Each tool's argument schema sets explicit upper bounds (`limit: maximum: 1000`); the dispatcher enforces schema validation. |
| R8 | The harness's `query_event_log` tool returns sensitive event data to an external client that should not see it. | High — audit boundary. | Medium. | Event payloads are already redacted at write time; the tool exposes only redacted views. Operator can disable the tool entirely via config (`mcp.server.tools.query_event_log.enabled: false`). |
| R9 | Sampling-depth runaway loops the harness's LLM. | High — runaway cost. | Low if explicit. | Per-session sampling-depth counter (default 4); exceeding emits typed error and unwinds. |
| R10 | Bundle reload mid-session removes a prompt / resource the client is mid-`get`/`read`. | Low — usability. | Medium. | Resolve at request time and pin the snapshot for the duration of the call. New calls see the new bundle state. |
| R11 | An external client floods the streamable-HTTP listener with handshake attempts (DoS). | Medium — local denial. | Medium. | Per-source rate-limit on handshake attempts (per-IP, simple token bucket). Configurable. |
| R12 | A tool implementation panics; the panic propagates and crashes the harness. | High — availability. | Low (Go's recover idiom). | Every handler wraps with `recover()`, logs the panic to event log + stderr, returns `InternalError` to the client. |

---

## 9. Open Questions for the User

These remain unresolved after research; resolving each materially shapes
implementation. Defaults from the spec are noted; the listed questions are
the ones the user explicitly flagged as `[NEEDS CLARIFICATION]` plus a
small handful surfaced during planning.

### Unresolved from the spec

1. **Bundle-contributed tool artifact kind (spec OQ-1)** — Do we ship
   `mcp_tool` as a registered bundle artifact kind in v1, or defer? Plan
   default (matches spec default): **defer the artifact kind contract
   to a follow-up mission**.

2. **Streamable-HTTP authentication default (spec OQ-2)** — When the
   operator opts the HTTP listener in but does not configure auth, do
   we (a) default to no-auth on loopback, (b) require a bearer token
   always, (c) emit a warning? Plan default (matches spec default):
   **(a) loopback-only no-auth allowed; non-loopback requires explicit
   bearer**.

3. **Sampling profile selection (spec OQ-3)** — When a tool issues a
   sampling request without a model hint, which provider profile? Plan
   default (matches spec default): **the bundle's `default_provider`;
   if absent, the first registered profile**.

4. **Resource size cap (spec OQ-4)** — What's the cap on
   `resources/read` payload size before we surface
   `ErrResourceTooLarge`? Plan default (matches spec default): **8 MiB,
   configurable**.

### Surfaced during planning

5. **Sampling architecture: internal LLM vs round-trip to external
   client's LLM** — see §6.4 note. Plan default if unresolved: **internal
   LLM under bundle profile**, matching FR-009.

6. **Concurrent session cap on streamable-HTTP** — protect against
   resource exhaustion. Plan default if unresolved: **16 concurrent
   sessions, configurable via `mcp.server.limits.max_concurrent_sessions`**.

7. **`run_bundle` tool's authorization** — exposing `run_bundle` to any
   MCP client lets that client trigger arbitrary harness sessions.
   Should it require an explicit allowlist of bundle ids per operator
   config? Plan default if unresolved: **yes — `mcp.server.tools.run_bundle.allowed_bundles:
   [...]` allowlist, empty = none allowed**.

8. **Default `query_event_log` retention scope** — should the tool
   expose only events from the current session, the current bundle, or
   any event? Plan default if unresolved: **scoped to the current
   session by default; operator may broaden via config**.

---

## Charter Check

Per `spec-kitty charter context --action plan` (loaded above):

- **DIRECTIVE_001 (Architectural Integrity)**: PASS by construction —
  every transport lives in its own sub-package; the server speaks only
  in-tree types; CI guard rule blocks cross-package transport-specific
  imports (§2, R1).
- **DIRECTIVE_003 (Decision Documentation)**: PASS — every material
  trade-off (sampling architecture, bearer-token auth model, JSON-RPC
  reuse, tool catalog scope) is recorded in this plan; an ADR will
  accompany the sampling-architecture decision.
- **DIRECTIVE_010 (Specification Fidelity)**: PASS — every FR/NFR/C is
  cited in the corresponding section.
- **DIRECTIVE_024 (Locality of Change)**: PASS — transport-by-transport
  and tool-by-tool decomposition keeps blast radius small.
- **DIRECTIVE_028 (Efficient Local Tooling)**: PASS — black-box tests
  via `httptest.Server` and recorded conformance fixtures.
- **DIRECTIVE_029 (Agent Commit Signing)**: applies at implementation
  time, not planning.
- **DIRECTIVE_030 (Test and Typecheck Quality Gate)**: tasks must
  enforce `go test ./... -race`, `go vet`, `golangci-lint`, and ≥ 80 %
  coverage on `core/mcp/server/**`.
- **DIRECTIVE_033 (Explicit Staging)**: applies at commit time.
- **DIRECTIVE_036 (Black-Box Testing)**: PASS — server tests drive the
  Server through its public MCP surface (JSON-RPC frames in/out);
  internal helpers are not asserted directly.

No charter conflicts to escalate.

---

## Phase 0 / Phase 1 artifact status

- **Phase 0 (`research.md`)**: GENERATED — see `research.md` plus
  `research/evidence-log.csv` and `research/source-register.csv`.
- **Phase 1 (`data-model.md`)**: GENERATED — see `data-model.md`. The
  contracts live in §3 of this plan.

---

## Branch contract — restated for hand-off

Feature branch: `feat/mcp-server-01KQ2WNV`. Planning base / merge target:
`feat/wire-integration`. All work ships via PR with ≥ 1 maintainer review
and squash-merge default. Suggested next command for the user:
`/spec-kitty.tasks --mission mcp-server-01KQ2WNV`.
