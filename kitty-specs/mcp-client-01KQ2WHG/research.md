# Research: MCP Client — Outbound Model Context Protocol Integration

**Mission**: `mcp-client-01KQ2WHG`
**Date**: 2026-04-25
**Researcher**: planning subagent (working from in-distribution knowledge of the
MCP protocol and the Go stdlib; 2026 ecosystem reading limited to what is
already known prior to this mission).

This is a deliberately brief research pass. The MCP protocol is well-known
and the day-one transports (stdio, HTTP+SSE, streamable-HTTP) all map to
Go-stdlib primitives plus a JSON-RPC layer. The two material decisions are
**SDK choice** (mcp-go vs hand-rolled minimal client) and **streamable-HTTP
client** (stdlib `net/http` is sufficient).

---

## 1. Protocol baseline

### 1.1 What MCP looks like on the wire

MCP is JSON-RPC 2.0 with a small set of canonical methods organized into
namespaces:

- **Lifecycle**: `initialize`, `notifications/initialized`, `ping`,
  `notifications/cancelled`, `notifications/progress`.
- **Tools**: `tools/list`, `tools/call`, `notifications/tools/list_changed`.
- **Prompts**: `prompts/list`, `prompts/get`,
  `notifications/prompts/list_changed`.
- **Resources**: `resources/list`, `resources/read`,
  `resources/templates/list`, `resources/subscribe`,
  `notifications/resources/list_changed`,
  `notifications/resources/updated`.
- **Logging**: `logging/setLevel`, `notifications/message`.
- **Sampling** (server → client request): `sampling/createMessage`.
- **Roots** (server → client request): `roots/list`,
  `notifications/roots/list_changed`.
- **Completion**: `completion/complete`.

The handshake: client sends `initialize` with its protocol version + capability
set; server responds with its own; client sends `notifications/initialized`.
Capability fields are advisory negotiation (e.g., `tools.listChanged: true`
opts the server into pushing tool-list-change notifications).

Protocol versions are date strings (`2025-06-18`, etc.). Mutual negotiation
is "highest matching" — client and server each advertise the highest version
they support; the lower of the two is the agreed version.

### 1.2 Day-one transports

- **stdio** — simplest. Client spawns server as a child process with stdin /
  stdout pipes. Each JSON-RPC frame is one newline-delimited JSON object on
  stdout / stdin. Stderr is an out-of-band logging channel; the spec says
  servers SHOULD route diagnostics there.

- **HTTP+SSE (legacy)** — older 2024 transport. Client opens an SSE stream to
  `GET /sse` for server-pushed messages, posts client-to-server messages to
  `POST /` with a session id. Two HTTP connections per session.

- **streamable-HTTP (current)** — 2025 transport. Single endpoint serves both
  request/response and streaming. Client posts JSON-RPC requests via HTTP
  `POST`; server responds with either a single JSON response or a chunked
  SSE-style streaming body (response framing is `text/event-stream` when
  streaming is needed). Session id carried via `Mcp-Session-Id` header.
  Replaces HTTP+SSE for new servers.

The mission's day-one set covers all three because the harness is the one
component that has to talk to whatever MCP servers the bundle declares.

### 1.3 Versioning and forward-compat

The MCP spec has an explicit forward-compat clause: clients MUST ignore
unknown methods on notifications and SHOULD return `MethodNotFound` for
unknown requests rather than abort. The client mission honors this.

---

## 2. Go SDK landscape

### 2.1 Decision question

Should the harness depend on a third-party MCP Go SDK, hand-roll a minimal
client, or both?

### 2.2 Options surveyed

**Option A — `mark3labs/mcp-go`** (community SDK, primary 2025 player)

- Pros: covers all three transports; struct-typed JSON-RPC contracts;
  active maintenance through 2025; integration story with `pkg.go.dev`.
- Cons: brings a dependency surface that isn't audited yet by this mission;
  may carry transitive deps the harness doesn't want (HTTP middlewares,
  test helpers); typed shapes may drift from the 2025-06-18 spec window
  before stabilizing; introduces a vendor-lock seam against MCP types
  inside the harness.
- Confidence: medium. The library exists; the question is whether its
  surface stays close enough to the protocol to act as a thin wrapper.

**Option B — hand-rolled minimal client** (stdlib only)

- Pros: full control of dependency footprint; types live in
  `core/mcp/client/` and align exactly with the harness's typed-error
  taxonomy; no third-party surface to audit for credential leaks
  (NFR-007); easier to verify "no provider SDK leak" invariant
  (DIRECTIVE_001).
- Cons: more code to maintain; less battle-testing on edge cases (large
  message reframing, malformed-JSON tolerance, SSE reconnection);
  no community fixture parity.
- Confidence: high. The protocol is small (≤ 30 methods) and the wire
  format is JSON-RPC; the hand-rolled cost is bounded.

**Option C — official `modelcontextprotocol/go-sdk`** (if it exists in 2026)

- The MCP organization has been incubating a Python SDK and a Typescript
  SDK; whether they have shipped an official Go SDK by 2026-04 is something
  I cannot verify in this research pass without a web fetch. I FLAG this
  as unresolved and default to Option B unless an authoritative reading at
  implementation time disproves my assumption.
- Confidence: low. I do not know with certainty.

### 2.3 Recommendation

**Hand-roll the minimal client (Option B).** Reasoning:

1. Charter Policy Summary: open-source core needs a small, well-understood
   dependency tree. An SDK that pulls in HTTP routers / auth helpers /
   metrics middleware violates the lightweight-by-default invariant.
2. DIRECTIVE_001 architectural integrity: a hand-rolled client's types
   live in `core/mcp/client/` directly; a third-party SDK's types would
   either become the wire-format types (vendor-lock) or be wrapped by
   parallel types (waste).
3. Bounded cost: the protocol is small. JSON-RPC 2.0 framing + the methods
   in §1.1 fit comfortably in ~1200 lines of Go.
4. The hand-rolled path gives us tight control over the audit emission
   contract — critical for FR-015 / NFR-007. Third-party SDKs sometimes
   round-trip request bodies on retry, which would risk plaintext
   credential leaks.

If at implementation time a battle-tested SDK is preferable, the WPs
intentionally isolate the JSON-RPC layer from the public `Pool` API so
swapping the underlying SDK is a one-package change.

### 2.4 What we DO take from outside

- **JSON-RPC 2.0 framing** — well-known message shapes; the harness's
  framing types live under `core/mcp/client/jsonrpc/`.
- **stdlib `os/exec`** for stdio child-process management.
- **stdlib `net/http`** for HTTP+SSE and streamable-HTTP transports.
- **stdlib `encoding/json`** for JSON marshal/unmarshal; `json.RawMessage`
  for opaque tool arguments / results (consistent with the existing
  `core/mcp/pool.go` shape).
- **`bufio.Scanner`** with a tuned buffer for newline-delimited stdio
  framing (the default 64 KB buffer is too small for large tool results;
  bump to 16 MiB ceiling).

---

## 3. Transport-specific notes

### 3.1 stdio

The reference. `os/exec.Cmd` with `StdinPipe()`, `StdoutPipe()`, and
`StderrPipe()`; one goroutine reads stdout and pumps JSON-RPC frames; one
goroutine reads stderr and emits `mcp/server_log` events; one goroutine
writes outbound frames to stdin. Process lifecycle managed by the Pool.

Edge cases that need explicit handling:

- A child process buffers stdout: the protocol mandates one JSON object per
  line and the harness MUST NOT assume framing alignment beyond "line per
  message." Use a `bufio.Scanner` with `Buffer(make([]byte, 0, 16*1024*1024))`
  and `MaxScanTokenSize(16<<20)`.
- Child crashes mid-call: the read goroutine sees EOF. The pool surfaces
  any pending requests with `ErrTransportFailure` (transient) and respawns
  per the per-server retry policy.
- Child exits with non-zero status: log the exit code as `mcp/server_exit`
  and surface to operator via the audit log.

### 3.2 HTTP+SSE (legacy)

Two HTTP connections per session: one long-lived `GET /sse` for the
server's push channel, one `POST` per outbound request. Session id assigned
by the server during handshake.

`net/http.Client` with `Transport.IdleConnTimeout` set generously and
`http.Request` with a `context.Context` that the pool can cancel. SSE
parsing is straightforward: each event is a series of `data:` lines
followed by a blank line; we accumulate and JSON-decode.

### 3.3 streamable-HTTP (modern)

One HTTP endpoint. POSTs return either `Content-Type: application/json`
(single response) or `Content-Type: text/event-stream` (streaming). Session
id in the `Mcp-Session-Id` header. Resumability after disconnect is part of
the spec (clients MAY include `Last-Event-ID` to resume), but resumability
is optional for v1 — if a streaming connection drops, the client surfaces
it as transient and the next call uses a fresh request.

Notes on `net/http`:

- Use `client.Do(req)` and detect `Content-Type` on the response. If
  streaming, read the body as SSE frames; if single-response, decode the
  body as JSON-RPC. No third-party HTTP library required.
- Set request timeouts via `context.Context`, not via
  `http.Client.Timeout` (Timeout cancels the body reader, which is what
  we want for streaming).
- TLS termination: the harness's HTTP client respects the system root
  store. mTLS is out of scope for v1 (enterprise-only future work, per
  C-005).

---

## 4. Concurrency model

The Pool fans out requests to multiple servers concurrently. Per server
there is one read goroutine, one write goroutine, and one in-flight-request
table keyed by JSON-RPC `id`. The read goroutine routes responses back to
the originating caller via per-call channels. Cancellation (FR-011) closes
the per-call channel and emits a `notifications/cancelled` to the server.

This is the canonical "JSON-RPC over a duplex stream" pattern. It does NOT
need a state machine library; a small Go struct + `sync.Mutex` suffices.

---

## 5. Sampling callbacks

Sampling (server → client) means the SERVER sends a `sampling/createMessage`
request. The client routes it to `core/llm` registry under the bundle's
selected provider profile (per spec OQ-1 default). The LLM connector is
already designed for this — it accepts a `GenerationRequest` and streams
back a `Stream` of events. The MCP client wraps the stream into the MCP
sampling response shape and returns it to the server.

Policy gate: a `PolicyGuard` (charter policy-engine integration) inspects
the sampling request before routing. v1 ships with a no-op guard that
allows everything; the policy-engine mission lands the real gate.

---

## 6. Replay determinism

Replay (event-log spec FR-009) requires that recorded MCP traffic returns
the same answer without re-spawning processes or making network calls. The
event log emits `mcp/tool_call_request` and `mcp/tool_call_response` pairs
that contain the full JSON-RPC envelopes. On replay, the Pool's
`replay-mode` swap returns the recorded responses from the log instead of
issuing the wire call. Implementation lives behind a `Pool` constructor
flag (`replay: true`) and is one file.

---

## 7. What this client does NOT do (out of scope)

- WebSocket transport (deferred — design accommodates per FR-017 but no
  day-one impl).
- mTLS / SSO bridge for HTTP transports (enterprise; deferred per C-005).
- MCP server-side traffic (out — see `mcp-server-01KQ2WNV` mission).
- OAuth / DCR (Dynamic Client Registration) for streamable-HTTP — the spec
  has an optional auth profile; out of scope for v1, deferred.
- Auto-discovery (`.well-known/mcp`) — out of scope.
- Protocol version below 2025-06-18 — the harness only speaks the current
  version and one previous (negotiation will pick whichever the server
  supports).

---

## 8. Honest uncertainty register

| # | Claim | Confidence | Backup if wrong |
|---|---|---|---|
| 1 | The official MCP Go SDK does not exist or is not stable as of 2026-04. | Low. | Cite the official SDK in plan §6 and reduce hand-rolled scope. |
| 2 | `mark3labs/mcp-go` is the most active community SDK. | Medium. | Pick another active SDK; the hand-rolled approach is unaffected. |
| 3 | `streamable-HTTP` displaces `HTTP+SSE` for new servers. | High. | If many servers in 2026 still ship HTTP+SSE, our parity matters and we keep both transports. |
| 4 | JSON-RPC 2.0 framing is unchanged. | High. | Trivially refactorable. |
| 5 | Hand-rolled JSON-RPC is < 1200 lines of Go. | Medium-high. | If it grows past 2000, evaluate SDK adoption mid-mission. |
| 6 | `bufio.Scanner` with 16 MiB buffer suffices for stdio framing. | High. | Switch to `bufio.Reader.ReadBytes('\n')` if scanner buffer pressure shows up. |

These uncertainties do not block planning — every one of them is escapable
inside the plan's package boundary without restructuring the harness.

---

## 9. Charter alignment

- DIRECTIVE_001 (architectural integrity): the hand-rolled JSON-RPC layer
  lives inside `core/mcp/client/jsonrpc/`; the public Pool surface in
  `core/mcp/client/` does not depend on any wire-format type.
- DIRECTIVE_028 (efficient local tooling): black-box tests via recorded
  wire fixtures; live network tests opt-in behind a flag.
- DIRECTIVE_036 (black-box testing): every test exercises the Pool's
  public API; transport-internal structs are not asserted directly.
- Performance benchmarks (charter): NFR-001 / NFR-002 / NFR-003 align with
  charter targets (RPC roundtrip < 50 ms p99 local).
- Local-first invariant: stdio works with zero network access; HTTP
  transports are explicitly opt-in via bundle declarations.
