# Feature Specification: MCP Client — Outbound Model Context Protocol Integration

**Feature Branch**: `feat/mcp-client-01KQ2WHG`
**Created**: 2026-04-25
**Status**: Draft
**Input**: User description: "Build an MCP (Model Context Protocol) client subsystem under `core/mcp/client/` that lets a kaneaz-harness session reach out to any number of MCP servers — local stdio child processes and remote HTTP/SSE / streamable-HTTP endpoints — declared by bundles. Deliver tool listing, tool invocation, prompt resolution, resource fetching, structured logging, sampling callbacks (MCP server asks the client to round-trip an LLM call), and roots negotiation. Pool lifecycle: open at session start, close at session end, replace on bundle reload. Auditable through the event log. Local-first: stdio first, network optional."

## User Scenarios & Testing *(mandatory)*

### User Story 1 — Bundle declares MCP servers and the harness uses their tools (Priority: P1)

A bundle author declares one or more MCP servers in bundle configuration. Each server declaration names the server, picks a transport (stdio child process, HTTP+SSE, or streamable-HTTP), and pins arguments / URL / environment. At session start the harness's MCP pool spins up every declared server, performs the MCP handshake (`initialize` → `initialized`), and exposes each server's advertised tools, prompts, and resources to the running agent. The agent invokes tools by `(server, tool_name)`; the client routes the call, marshals arguments, awaits the response, and returns the result.

**Why this priority**: Tool calling against MCP servers is the primary externalization seam for the harness — without it, the configuration-first promise rots into a closed agent runtime. Stdio specifically is the lowest-friction transport an operator can wire up locally and is the de-facto reference transport for the protocol.

**Independent Test**: A test bundle declares one stdio MCP server (a tiny "echo" Go binary in `testdata/`) and one HTTP server (a recorded fixture). The agent issues `tools/list` and `tools/call` against each; both succeed and the responses are returned to the agent unchanged.

**Acceptance Scenarios**:

1. **Given** a bundle declares two stdio MCP servers, **When** the session opens, **Then** both processes are spawned, the `initialize` exchange completes for each, and `Tools()` returns the union (server-prefixed) of both servers' tools.
2. **Given** a bundle declares an HTTP+SSE MCP server, **When** the session opens, **Then** the client establishes the HTTP control channel and the SSE event stream and returns the server's tool list within the configured timeout.
3. **Given** a tool call returns a structured JSON result, **When** the agent invokes `Call(server, tool, args)`, **Then** the client returns the JSON-encoded result without modification beyond MCP framing.
4. **Given** an MCP server fails to initialize (non-zero exit, handshake timeout, protocol-version mismatch), **When** the pool opens, **Then** that one server is marked `unhealthy`, the failure is reported with server id + cause, and other servers continue to operate normally.

---

### User Story 2 — MCP server features beyond tools work end-to-end (Priority: P1)

The MCP protocol exposes more than tool calling: prompts (server-templated messages), resources (server-hosted documents identified by URI), structured server logging, and sampling (the server asks the client to invoke an LLM on its behalf). The harness's MCP client surfaces all four to the rest of the runtime so a bundle can include MCP servers that ship prompts and reference documents — not just tools.

**Why this priority**: Tools alone collapse MCP into "function calling with extra steps." The protocol's prompt + resource + sampling features are exactly what makes a bundle ecosystem viable: a server can ship a complete domain pack — prompts, retrieval surfaces, and a sampling-driven planner — without building its own LLM glue.

**Independent Test**: A test MCP server in `testdata/` advertises one prompt, one resource, and a sampling-requesting tool. The agent fetches the prompt, fetches the resource, and invokes the sampling tool. The sampling callback flows back through the harness's LLM connector, the model produces a completion, and the server's tool returns it.

**Acceptance Scenarios**:

1. **Given** an MCP server advertises a prompt named `summarize_doc`, **When** the agent calls `GetPrompt(server, "summarize_doc", args)`, **Then** the server-rendered messages are returned to the agent.
2. **Given** a server advertises resources, **When** `ListResources(server)` is called, **Then** every resource URI is returned, and `ReadResource(server, uri)` returns the bytes + MIME type.
3. **Given** a server invokes a tool that internally requests a sampling callback, **When** the sampling request reaches the client, **Then** the client routes it to the harness LLM connector under the bundle's resolved provider profile and returns the model's response back to the server.
4. **Given** an MCP server emits a logging notification at level `warn`, **When** the notification arrives, **Then** the client emits an `mcp/server_log` event with the server id, level, and message into the harness event log.

---

### User Story 3 — Every MCP interaction is auditable and replayable (Priority: P1)

Every MCP `initialize` exchange, every tool call, every notification, every error becomes an append-only entry in the harness event log with credentials redacted. An operator can later replay a session, branch from a prior step, audit "what tool did the model actually call" and "what did the server return," and confirm no secrets travelled in the clear.

**Why this priority**: This is the harness's audit story and a SOC 2-readiness anchor in the project charter (charter Policy Summary). Without it, MCP becomes a black-box side-channel in an otherwise auditable runtime.

**Independent Test**: An operator runs a multi-step agent session against a stdio MCP server, then queries the event log for that session and confirms it can reconstruct the full handshake, every tool call's arguments and result, and every notification — with no plaintext credentials anywhere in the log.

**Acceptance Scenarios**:

1. **Given** a session opens MCP servers and invokes one tool, **When** the operator reads the event log, **Then** entries exist for: pool open, per-server `initialize`, tool list, tool call (with arguments redacted of credential patterns), tool response, and pool close.
2. **Given** a server's environment contains a credential reference, **When** the pool spawns the server, **Then** the resolved credential value does not appear in any event log entry (only the credential reference's kind + locator).
3. **Given** a tool call fails on the server, **When** the entry is written, **Then** the failure entry includes the protocol-level error and the chain remains internally consistent (append-only, no rewrites).
4. **Given** a session is replayed, **When** the replay reaches an MCP tool call, **Then** the recorded response is returned to the agent without re-invoking the server (replay determinism per event-log spec).

---

### User Story 4 — Transient transport failures recover without breaking the session (Priority: P2)

When a stdio child process crashes mid-session, or an HTTP/SSE connection drops, or a streamable-HTTP request times out, the client surfaces the failure as a typed transient error, attempts a bounded reconnect (per-server retry policy with exponential backoff), and keeps other servers running. Non-transient failures (handshake protocol mismatch, server returns `ErrInvalidRequest`) are surfaced immediately without retry.

**Why this priority**: Local stdio servers can crash; remote MCP services can blip. Without retry, every flaky tool call cascades into a session abort. Bundle authors expect tool calls to be at-most slightly delayed under load, not fatal.

**Independent Test**: A fault-injecting fixture stdio server crashes after the first tool call. The client detects the EOF, respawns the process, completes the handshake, and the next tool call succeeds. The event log shows the crash, the respawn, and the recovered call.

**Acceptance Scenarios**:

1. **Given** a stdio server's child process exits with a transient error code, **When** the next request arrives, **Then** the client respawns the process within the per-server retry budget and resumes routing requests.
2. **Given** an SSE connection drops mid-stream, **When** reconnect succeeds, **Then** the client re-issues the `initialize` exchange and continues serving tool calls; in-flight calls without delivered chunks are retried, in-flight calls with partial content are surfaced as transient failures (no double-bill / duplicate side-effects).
3. **Given** a server returns a non-transient protocol error (`MethodNotFound`, `InvalidParams`), **When** the client receives the response, **Then** the error is surfaced to the caller without retry.
4. **Given** the retry budget is exhausted, **When** the next attempt fails transiently, **Then** the server is marked `unhealthy` and removed from the active pool until the next bundle reload.

---

### User Story 5 — Bundle reload swaps the pool atomically (Priority: P2)

When a bundle reload changes the set of declared MCP servers, the client tears down servers no longer declared, spawns servers newly declared, and leaves untouched servers running so in-flight tool calls do not see their pool yanked from under them. After reload completes, the new pool is the only routing target.

**Why this priority**: Bundles are durable, swappable artifacts; tearing the entire pool down on every reload defeats the local-first ergonomics ("typing into a different bundle should not kill my running tool calls").

**Independent Test**: A test toggles between two bundle states — A {servers x, y} and B {servers y, z}. After reload from A → B, server x is closed, server z is opened, server y is preserved. The audit log shows exactly those three lifecycle events.

**Acceptance Scenarios**:

1. **Given** a bundle reload changes the declared server set, **When** the pool reloads, **Then** removed servers receive a graceful shutdown (handshake termination + close), added servers complete `initialize`, and unchanged servers retain their state.
2. **Given** a reload encounters a server whose new declaration differs only in arguments, **When** the pool reloads, **Then** the existing process is terminated and respawned with the new arguments (no in-place mutation).
3. **Given** a reload fails for one new server, **When** the pool finishes the reload pass, **Then** that server is marked `unhealthy` but the rest of the new pool comes up; the prior pool's already-removed servers are not resurrected.

---

### User Story 6 — A new transport is added without modifying core (Priority: P3)

A contributor adds support for a new MCP transport (e.g., websockets, named pipes, an enterprise-only connection scheme) by implementing the transport contract and registering it. No changes are required to the core packages outside the transport's own package.

**Why this priority**: The MCP spec admits transport plurality; pinning the harness to two transports forever forecloses on the enterprise distribution split (charter Deployment Constraints) and on community-contributed transports.

**Independent Test**: A throwaway "in-memory" transport is implemented in a separate package and registered. A bundle declares an MCP server using it; the agent invokes a tool; all of this lands without any commit touching `core/mcp/client/` interface package or any other `core/` package.

**Acceptance Scenarios**:

1. **Given** a new transport implements the transport contract and registers itself, **When** a bundle declares an MCP server with that transport kind, **Then** it can be opened and routed through the same `Pool` API.
2. **Given** an attempt to add a new transport that requires modifying the shared `core/mcp/client` interface or any other `core/` package, **When** the change is reviewed, **Then** the review flags the architectural-integrity violation before merge.

---

### Edge Cases

- A stdio MCP server prints non-JSON garbage on stderr: the client must treat stderr as a logging channel (forward to event log under `mcp/server_log`) and never confuse it with the JSON-RPC response stream on stdout.
- An MCP server's `initialize` returns a protocol version newer than the client's max supported version: the client negotiates down to the highest mutually-supported version per the MCP version-handshake rules; if no overlap, the server is marked unhealthy.
- Two bundles overlay-declare the same server name with different transport definitions: the bundle resolver's conflict-detection path surfaces the conflict before the pool ever opens (delegated to upstream `bundle-format-resolver` FR-009 conflict semantics).
- A tool call's arguments include a credential pattern (e.g., the agent generated `"sk-ant-..."` text in input): the redaction pipeline matches on the request entry before persistence; the wire payload is unmodified.
- An MCP server advertises 10,000 tools: the client paginates `tools/list` per protocol; the pool surfaces the full set without issuing 10,000 round-trips to the agent at session start.
- The harness machine goes offline (laptop closed) while a long-running tool call is in flight: on resume, the in-flight call is marked cancelled, the pool is reopened, and the session may retry per its policy (out of scope for this spec, but the client must not block resume).
- A sampling callback arrives but the bundle has no LLM provider profile configured: the client returns a typed `ErrSamplingUnavailable` to the server within the protocol's error envelope; no implicit profile is selected.
- A server emits a notification with an unknown `method`: the client logs an `mcp/protocol_warning` event and continues; unknown notifications never abort the session (forward-compatibility).

---

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001** — Day-one transports: stdio (child process), HTTP+SSE (sse), streamable-HTTP. WebSocket and other transports out of scope for v1.
- **FR-002** — Bundle artifact `kind: mcp_server` declares one MCP server. Fields: id, transport kind, command/args/env (stdio) OR URL + headers credential references (HTTP).
- **FR-003** — Indirect credential references only for HTTP headers (no plaintext API tokens in declarations); references resolved via `core/secrets`.
- **FR-004** — `initialize` / `initialized` handshake with capability + protocol-version negotiation per MCP spec.
- **FR-005** — `tools/list` and `tools/call` round-trip with structured argument & result JSON.
- **FR-006** — `prompts/list` and `prompts/get` round-trip; rendered messages returned to caller.
- **FR-007** — `resources/list`, `resources/read`, `resources/templates/list` round-trip; bytes + MIME returned.
- **FR-008** — Server-initiated logging notifications (`notifications/message`) routed into harness event log.
- **FR-009** — Sampling: server-initiated `sampling/createMessage` requests routed to harness LLM connector and result returned to server.
- **FR-010** — Roots negotiation: client advertises filesystem-style roots the server may operate on, scoped to bundle declarations (no implicit elevation).
- **FR-011** — Cancellation: in-flight requests honor `context.Context` cancellation; the underlying transport is closed within 1 second p99.
- **FR-012** — Distinguish transient (transport drops, EOF, 5xx, 429) from non-transient (`MethodNotFound`, `InvalidParams`, version mismatch) errors via typed taxonomy.
- **FR-013** — Per-server retry policy with exponential backoff + full jitter; configurable in bundle declaration.
- **FR-014** — Pool lifecycle: `Open(specs)`, `Close()`, `Reload(newSpecs)`; reload preserves unchanged servers.
- **FR-015** — Audit emit: every handshake, request, response, notification, error, and lifecycle transition lands in the event log under the `mcp/` kind namespace.
- **FR-016** — Replay: a recorded session's MCP responses are returned without re-invoking the server (event-log spec FR-009 alignment).
- **FR-017** — Pluggable transport contract: new transports added in their own package; no central registry edits.
- **FR-018** — Capability gate: tool calls against absent / unknown servers return `ErrServerUnknown`; calls against tools the server didn't advertise return `ErrToolUnknown` before any wire call.
- **FR-019** — Pre-flight: every declared server's credential references are resolved (or their failure reported) before pool open completes.
- **FR-020** — Tool result truncation policy: results exceeding the bundle's declared max size are surfaced with a typed `ErrResultTooLarge` and the truncated payload is *not* silently delivered.

### Non-Functional Requirements

- **NFR-001** — Pool open latency for N stdio servers: < 200 ms × N p95 on a modern laptop, parallel spawn allowed.
- **NFR-002** — Tool-call overhead (client side): < 5 ms p95 above raw transport latency.
- **NFR-003** — Cancellation responsiveness: < 1 s p99 from `ctx.Done()` to socket / pipe close.
- **NFR-004** — Pool memory footprint: < 16 MB resident per stdio server idle (excluding the server process itself).
- **NFR-005** — Pre-flight credential resolution must NOT block process start by more than 500 ms p95 across the full pool.
- **NFR-006** — Reload completion: < 1 s p95 for a typical pool delta (≤ 5 changes).
- **NFR-007** — No plaintext credential bytes ever appear in event log entries.
- **NFR-008** — Redaction recall ≥ 99 % across known credential patterns at v1.
- **NFR-009** — Append-only invariant: log entries written by the MCP client are never edited or deleted in place.
- **NFR-010** — All FR-001 transports parity: tool calling and prompts work identically across stdio / HTTP+SSE / streamable-HTTP.
- **NFR-011** — Replay-determinism: replay does not require network access for HTTP transports nor child-process spawn for stdio.
- **NFR-012** — Architectural-integrity: nothing outside `core/mcp/client/<transport>/` imports a transport-specific library.

### Constraints

- **C-001** — `core/mcp/client/` is the only seam through which the rest of the harness reaches MCP servers; Wails / RPC / UI never call transports directly.
- **C-002** — No inline plaintext credentials in any bundle declaration of an MCP server (defense-in-depth alignment with `secrets-keychain` FR-015).
- **C-003** — All logging / notifications go through the harness event log; the MCP client does NOT own a parallel log file.
- **C-004** — MCP server declarations live as bundle artifacts of a registered kind, not as a top-level configuration surface.
- **C-005** — Provider isolation: the open-source core ships stdio + HTTP+SSE + streamable-HTTP transports; enterprise-only transports may live in build-tagged packages without forking core.
- **C-006** — Local-first: nothing in the client mandates a network round-trip; stdio MUST work with zero network access (charter Deployment Constraints).
- **C-007** — Sampling callbacks are gated by the bundle's policy engine; a sampling request that fails policy is denied with a typed protocol error (not an opaque server failure).

### Key Entities

- **Server Declaration** — bundle artifact `kind: mcp_server`. Fields: id, transport, command/url/env, credentials (refs only), retry policy, max result size.
- **Pool** — runtime aggregate of opened MCP server connections; lifecycle Open/Close/Reload.
- **Connection** — one live MCP transport bound to one server; owns the JSON-RPC send/receive loop.
- **Tool Reference** — `(server_id, tool_name)` tuple; carried in agent → MCP routing.
- **Sampling Request** — server-initiated message inviting the client to call its LLM connector and return the model response.
- **Roots** — filesystem path scopes the bundle grants the server; negotiated during handshake.
- **Notification** — server-initiated push; logging, progress, resource-changed, prompt-changed.

---

## Assumptions

1. The MCP protocol version we target is the current 2025-06-18 revision plus the streamable-HTTP transport added later in 2025; the client negotiates down on connect.
2. Bundle authors declare MCP servers; agents do not dynamically spawn servers at runtime.
3. The `core/secrets` subsystem and `core/event` subsystem are available via interfaces; the MCP client does not own credential storage or log persistence.
4. Sampling callbacks reuse the harness's `core/llm` registry; the bundle's selected provider profile is the sampling target unless the server requests a specific model and the policy engine permits it.
5. Tool call schemas and prompt argument schemas are JSON Schema; the client validates request shape before sending and result shape on receive (best-effort — invalid schema gates the tool call with a typed error, never silently mangles the wire body).
6. Out-of-process MCP servers may be written in any language and invoked via stdio; the client does not require Go-implemented servers.

## Open Questions

1. **OQ-1 — Sampling fan-out policy** — when a sampling callback arrives, do we honor the server's `model` hint or always route through the bundle's default provider profile? Default if unresolved: prefer the bundle's default profile, fall back to the hint only when the policy engine permits arbitrary model selection.
2. **OQ-2 — Roots scope source** — derive roots from the bundle's declared `paths` field, from the harness's `dataDir`, or from explicit per-server `roots:` configuration? Default if unresolved: explicit per-server `roots:` configuration only, no implicit fallback.
3. **OQ-3 — Transport fallback** — if the streamable-HTTP transport fails handshake, do we silently downgrade to HTTP+SSE? Default if unresolved: no — transport kind is explicit; fallback is the operator's choice expressed by declaring two MCP servers.

## Success Criteria

- **SC-001** — All five day-one transports (stdio, HTTP+SSE, streamable-HTTP for v1; future-ready for two more) pass an end-to-end "list, call, prompt, resource, sampling" black-box matrix.
- **SC-002** — Pool open / reload / close lifecycle test passes against a 5-server fixture with mixed transports.
- **SC-003** — Audit suite: zero plaintext credentials across the full transport matrix; redaction tests cover Anthropic, OpenAI, Bedrock, AWS-V4 signature, and bearer token shapes.
- **SC-004** — Replay determinism: a recorded session replays without spawning child processes or opening sockets.
- **SC-005** — Charter ≥ 80 % line coverage on `core/mcp/client/**` per testing standards.
