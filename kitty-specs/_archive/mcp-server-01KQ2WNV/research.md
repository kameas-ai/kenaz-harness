# Research: MCP Server — Inbound Model Context Protocol Surface

**Mission**: `mcp-server-01KQ2WNV`
**Date**: 2026-04-25
**Researcher**: planning subagent (working from in-distribution knowledge of
the MCP protocol, the Go stdlib, and the kaneaz-harness charter; 2026
ecosystem reading limited to what is already known prior to this mission).

This research pass mirrors the MCP-client mission's structure: the protocol
is well-known, the day-one transports map cleanly to Go stdlib primitives,
and the material decisions are **SDK choice** and **HTTP server library
choice**. Both default to "hand-rolled, stdlib-only," consistent with the
client mission, for the reasons in §2.

The mission's surface is also informed by reuse: the JSON-RPC framing layer
and the `Transport` contract pattern from the MCP-client mission
(`mcp-client-01KQ2WHG`) are reusable. This mission documents how reuse is
structured without creating circular dependencies.

---

## 1. Protocol baseline

### 1.1 What MCP looks like on the wire (server side)

Same JSON-RPC 2.0 method set as the client mission documents. The
asymmetry is in **who issues which request**:

- The CLIENT issues: `initialize`, `tools/list`, `tools/call`,
  `prompts/list`, `prompts/get`, `resources/list`, `resources/read`,
  `resources/templates/list`, `logging/setLevel`, `completion/complete`,
  `roots/list_changed` (notification).
- The SERVER issues: `sampling/createMessage` (request to client),
  `roots/list` (request to client), `notifications/message` (logging
  push), `notifications/tools/list_changed`, `notifications/prompts/
  list_changed`, `notifications/resources/list_changed`,
  `notifications/resources/updated`.

The server's main job is implementing handlers for the client-issued
methods and managing the bidirectional JSON-RPC state to issue
server→client requests when needed (e.g., a tool implementation needs
sampling).

### 1.2 Day-one transports for the server

- **stdio** — the harness is launched as a child process by an external
  MCP client (Claude Desktop, Cursor, or a custom agent runner). The
  external client owns the spawn; the harness reads JSON-RPC frames from
  stdin and writes them to stdout. **Critical**: stdout is the protocol
  channel — internal logging MUST go to stderr or the event log.

- **streamable-HTTP (current)** — the harness binds an HTTP server (off
  by default; opt-in via configuration). External clients POST JSON-RPC
  requests; the server responds with either `application/json`
  (single-response) or `text/event-stream` (streaming) depending on
  whether the response is incrementally produced.

The legacy HTTP+SSE transport is NOT exposed server-side in v1 (per spec
FR-001). The MCP-client mission keeps it for outbound compatibility with
older servers; inbound surfaces converge on streamable-HTTP.

### 1.3 Authentication on inbound

The MCP spec admits transport-level auth (the spec is largely
agnostic but surfaces "you SHOULD authenticate streamable-HTTP" guidance).
Day-one auth options for the harness:

- **Loopback no-auth** (default when bound to `127.0.0.1`): trusted by
  the OS-level boundary that nothing on `lo` is hostile.
- **Bearer token**: configured via a `core/secrets` reference; the
  harness checks `Authorization: Bearer <token>` against the resolved
  value. Required when bound to non-loopback.

Anything more sophisticated (mTLS, OAuth, SSO) is enterprise-only and
deferred per C-005.

---

## 2. Go SDK landscape (server side)

### 2.1 Decision question

Same question as the client mission: third-party MCP Go SDK, hand-rolled,
or both?

### 2.2 Options surveyed

The same SDKs apply (mark3labs/mcp-go primary; an official
`modelcontextprotocol/go-sdk` may exist but verification deferred). The
trade-offs mirror the client side:

**Option A — community SDK**: faster bring-up, but pulls in HTTP routers
or middleware that conflict with the harness's lightweight default.

**Option B — hand-rolled**: full control, dependency-light, matches the
client mission's choice. The protocol is small.

**Option C — official Go SDK** (if exists in 2026): preferable if it
ships, but treat as unverified.

### 2.3 Recommendation

**Hand-roll the server, REUSE the MCP-client's JSON-RPC framing layer.**

The MCP-client mission lands `core/mcp/client/jsonrpc/` with framing
types and canonical method param/result struct types. The server can
import and reuse this package directly — JSON-RPC framing is symmetric.
If reusing the package across client + server creates an inconvenient
naming choice (`client/jsonrpc/`), refactor to `core/mcp/jsonrpc/` as
a one-time move during this mission's WP02. The plan accommodates either.

### 2.4 What we DO take from outside

- **JSON-RPC 2.0 framing** — reused from MCP-client mission via shared
  package.
- **stdlib `net/http`** for streamable-HTTP server (`http.Server` +
  `http.Handler`).
- **stdlib `os.Stdin` / `os.Stdout` / `os.Stderr`** for stdio mode.
- **stdlib `bufio`** for line-delimited stdio framing.
- **stdlib `encoding/json`**.

### 2.5 HTTP server library

`net/http` is sufficient. We do NOT need:

- A router (only one or two handler paths).
- A middleware framework (auth check + logging are inline functions).
- A WebSocket library (no WebSocket transport).
- A gRPC framework (this is JSON-RPC over HTTP).

This keeps the dependency footprint as light as the client mission's.

---

## 3. Transport-specific notes

### 3.1 stdio

The harness's `mcp serve --stdio` CLI subcommand connects `os.Stdin` /
`os.Stdout` to the MCP server's session loop. Internal logging routes
to `os.Stderr` and the event log only. The startup gate must verify
stdout is suitable: if the harness has been launched with stdout pointed
at a terminal that does ANSI colorization or buffering, refuse to start
to avoid corrupting the protocol.

Edge cases:

- The external client closes stdin: server treats as session end, drains
  any in-flight handlers (with timeout), exits cleanly.
- The external client never sends `initialize`: timeout after
  `mcp.server.handshake_timeout` (default 30 s) and exit gracefully.
- A handler panics: recover, return `InternalError` to the client, log
  to event log + stderr.

### 3.2 streamable-HTTP (server side)

`http.Server` listening on a configurable bind address (`127.0.0.1:0`
default). One handler at `/` that:

- Routes `POST` to JSON-RPC dispatch.
- Generates a session id on first `initialize` and tracks it via
  `Mcp-Session-Id` header.
- Decides response framing based on whether the handler produces
  incremental output (streaming = `text/event-stream`, single = `application/json`).
- Performs origin / allowlist checks (FR-016) and bearer-token auth
  (FR-017) before dispatch.

Server lifecycle: graceful shutdown on `core.Core.Shutdown`. The drain
algorithm (FR-020): stop accepting new connections, wait up to
`drain_timeout` (default 30 s) for in-flight handlers to complete, then
hard-close.

### 3.3 Origin / allowlist policy

`http.Request.Header.Get("Origin")` is checked against a configurable
allowlist. Default allowlist: `null`, `localhost`, `127.0.0.1`. Bound to
non-loopback flips the policy to require explicit allowlist entries.
Rejected origins land in the event log as `mcp.server/origin_denied`.

---

## 4. Concurrency model

The server fans out one goroutine per session. Per-session state:

- Session id (ULID; generated at handshake).
- Negotiated protocol version + capability set.
- In-flight handler tracking (for cancellation propagation).
- Outgoing notification queue (for server-pushed messages).
- Connection-info snapshot (client info, source address for HTTP).

This mirrors the client's connection state machine but inverted — the
server owns the lifecycle from the inbound connection's perspective.

---

## 5. Tool registration

V1 ships harness-native tools as Go implementations of a `Tool`
interface:

```go
type Tool interface {
    Name() string
    Description() string
    InputSchema() json.RawMessage
    Call(ctx context.Context, args json.RawMessage, sess Session) (ToolResult, error)
}
```

Each tool is registered with a `Catalog`. The day-one catalog (FR-018):

- `run_bundle` — start a session against a named bundle (mutating).
- `list_bundles` — list resolved bundles (read).
- `query_event_log` — read from event log with filters (read).
- `list_sessions` — list active sessions (read).
- `list_provider_profiles` — list configured LLM profiles (read).

Tools route into existing harness subsystems via dependency injection:

- `run_bundle` → `core/session.Executor.Start`.
- `list_bundles` → `core/bundle.Resolver.List`.
- `query_event_log` → `core/event.Log.Query`.
- `list_sessions` → `core/session.Executor.List`.
- `list_provider_profiles` → `core/llm.Registry.List`.

A future bundle artifact `kind: mcp_tool` (deferred per spec OQ-1)
would register tools dynamically; design accommodates.

---

## 6. Prompt and resource sourcing

Active bundle prompts and resources are sourced from the bundle resolver
at request time (not cached, since the bundle set may reload). For each
`prompts/list` / `resources/list` request, the server queries the
resolver's current `ResolvedGraph` and assembles the response.

This matches the bundle-format-resolver mission's contract: prompts and
resources are bundle artifacts and the resolver exposes them via a
`ResolvedBundle.Prompts()` / `ResolvedBundle.Resources()` accessor.

---

## 7. Sampling callbacks (server-side)

When a tool implementation needs an LLM call, it issues a sampling
request through the session's `Sample(ctx, req)` method. Implementation:

1. Server-side handler receives the sampling request.
2. Session looks up the bundle's selected provider profile via
   `core/llm.Registry.Profile(id)`.
3. Policy gate (charter) inspects the request; deny → `ErrPolicyDenied`.
4. Server issues `sampling/createMessage` to the EXTERNAL client over
   the session.
5. External client routes through ITS LLM (this is the standard MCP
   pattern: the client owns the LLM). Result returns over the session.
6. Tool handler completes the `tools/call` response with the sampling
   result.

**Note**: there's an alternative model where the server uses ITS OWN LLM
(via `core/llm`) for sampling rather than asking the external client.
The spec's intent is the former (client owns the LLM) but a kaneaz-
harness deployment might want internal-LLM sampling for cost/policy
reasons. Spec FR-009 describes the internal-LLM model; this research
flags the ambiguity and the plan resolves toward "use the internal LLM
under the bundle's profile" (matches FR-009 and OQ-3 default). Tools
that prefer to round-trip the external client's LLM can do so by
issuing a regular sampling request to the session.

The plan resolves this ambiguity in §6.4.

---

## 8. Replay determinism

Inbound MCP sessions need replay-aware audit. Unlike the client mission,
where replay returns recorded responses without making wire calls, the
server's replay is more nuanced: tools have side effects (`run_bundle`
mutates state). Replay treats inbound MCP entries as **observations**,
not invocations: the event log records the tool call for audit, but
replay does NOT re-execute the tool. The mission ships a "replay
recorder" that emits sufficient context per tool call (parameters,
session id, client info, result) for full audit reconstruction.

---

## 9. What this server does NOT do (out of scope)

- HTTP+SSE inbound transport (deferred — design accommodates).
- WebSocket transport.
- mTLS / SSO bridge for HTTP transports (enterprise; deferred per C-005).
- OAuth / DCR.
- Bundle-contributed tools via `kind: mcp_tool` artifact (deferred
  per spec OQ-1; design accommodates).
- Server-side `roots/list_changed` push to clients on bundle reload (the
  spec accommodates but v1 omits — clients can poll on next handshake).

---

## 10. Honest uncertainty register

| # | Claim | Confidence | Backup if wrong |
|---|---|---|---|
| 1 | Reusing `core/mcp/client/jsonrpc/` from the server is clean (same JSON-RPC framing). | High. | Refactor to `core/mcp/jsonrpc/` if name clarity demands it; one-time move. |
| 2 | Stdlib `net/http.Server` suffices for streamable-HTTP. | High. | Replace with `golang.org/x/net/http2` if HTTP/2 push is needed; not currently. |
| 3 | The MCP test client / conformance suite exists. | Low. | Build our own black-box conformance fixture if no upstream exists. |
| 4 | Harness-native tools' day-one catalog (FR-018) is correct in scope. | Medium. | Trim during WP planning if a tool turns out to be fundamentally hostile to MCP exposure (e.g., privilege escalation surface that operators must explicitly opt into). |
| 5 | Internal-LLM sampling (vs round-trip to external client's LLM) is the right default. | Medium. | Plan §6.4 calls this out; can be flipped via a config flag if operator feedback differs. |
| 6 | `query_event_log` exposed read-only is safe. | Medium. | Audit log itself is sensitive; the tool MUST honor redaction (events are already redacted at write time, so safe to expose); double-check during WP07. |

---

## 11. Charter alignment

- DIRECTIVE_001 (architectural integrity): the server lives in
  `core/mcp/server/`; transport-specific code in `core/mcp/server/<transport>/`.
- DIRECTIVE_028 (efficient local tooling): black-box tests via
  `httptest.Server`; conformance fixtures in `testdata/`.
- DIRECTIVE_036 (black-box testing): every test exercises the server
  through its public MCP surface (JSON-RPC frames in/out).
- Performance benchmarks (charter): NFR-001 / NFR-002 align with charter
  targets.
- Local-first invariant: stdio works with zero network access; HTTP
  listener defaults OFF and to loopback only.
