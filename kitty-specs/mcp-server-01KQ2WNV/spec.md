# Feature Specification: MCP Server — Inbound Model Context Protocol Surface

**Feature Branch**: `feat/mcp-server-01KQ2WNV`
**Created**: 2026-04-25
**Status**: Draft
**Input**: User description: "Build an MCP (Model Context Protocol) server subsystem under `core/mcp/server/` that exposes the kaneaz-harness's own tools, prompts, resources, and sampling surface to external MCP clients (Claude Desktop, Cursor, custom agents). Day-one transports: stdio child-process mode and streamable-HTTP. Surface harness-native tools (run-bundle, scheduler ops, event-log query), bundled prompts/resources from currently-resolved bundles, sampling callbacks (route the external client's sampling requests through the harness LLM connector). Auditable through the harness event log. Local-first: stdio first, network optional and disabled by default."

## User Scenarios & Testing *(mandatory)*

### User Story 1 — External MCP client uses harness tools via stdio (Priority: P1)

An external MCP client (Claude Desktop, Cursor, an agent in another runtime) launches `kaneaz-harness mcp serve --stdio` as a child process. The harness performs the MCP `initialize` handshake, advertises a curated set of harness-native tools (e.g., `run_bundle`, `query_event_log`, `list_sessions`), and accepts `tools/call` invocations. The harness executes the tool against its in-process subsystems and returns the result.

**Why this priority**: Stdio is the de-facto transport for desktop MCP clients and is the lowest-friction integration path. Without it, the harness cannot be wired into Claude Desktop or Cursor without bespoke shims, defeating the configuration-first promise.

**Independent Test**: A test harness launches `kaneaz-harness mcp serve --stdio`, performs the handshake by writing JSON-RPC frames to stdin and reading from stdout, calls `tools/list`, picks a known tool, calls `tools/call`, and asserts the response payload matches the expected harness output.

**Acceptance Scenarios**:

1. **Given** the harness is launched in stdio MCP server mode, **When** the client writes a valid `initialize` request, **Then** the harness writes an `initialize` response advertising its protocol version, server info, and capabilities; the harness does NOT consume stdout for any other purpose.
2. **Given** the handshake completes, **When** the client requests `tools/list`, **Then** the harness returns the curated tool catalog (every tool tagged with the harness subsystem it routes into).
3. **Given** the client invokes `tools/call` with valid arguments for a known tool, **When** the harness routes the call internally, **Then** it returns a structured tool result containing the harness output.
4. **Given** the client invokes `tools/call` with arguments that fail JSON Schema validation, **When** the harness checks the request, **Then** it returns a typed `InvalidParams` error WITHOUT executing the tool.

---

### User Story 2 — External MCP client uses streamable-HTTP transport (Priority: P1)

An operator opts the harness's network MCP listener into streamable-HTTP mode for use by remote MCP clients on the same machine or LAN (gated by an explicit operator opt-in; off by default). The harness binds to localhost (or an operator-chosen interface), performs the streamable-HTTP MCP handshake, and accepts the same tool / prompt / resource / sampling traffic as the stdio mode.

**Why this priority**: Streamable-HTTP is the modern remote MCP transport (introduced 2025) and is a precondition for making the harness a peer to Claude API's hosted MCP support and for any multi-tenant LAN deployment. Without it, the only network option is the older HTTP+SSE flow which the protocol increasingly deprecates.

**Independent Test**: An HTTP fixture client opens a streamable-HTTP MCP session against a locally-bound harness, performs `initialize` → `tools/list` → `tools/call`, asserts the streamed responses arrive correctly framed, and the session closes cleanly.

**Acceptance Scenarios**:

1. **Given** the harness is configured with `mcp.server.http.enabled: true` and bound to localhost, **When** an HTTP MCP client posts the initialize handshake, **Then** the harness replies with the negotiated protocol version and the streamable-HTTP session id.
2. **Given** the streamable-HTTP listener is bound, **When** an external client connects from a non-allowed origin / IP, **Then** the harness rejects the connection per the operator's CORS / allowlist settings.
3. **Given** a long-running tool call (over 30 seconds) is in flight, **When** the streamable-HTTP request is open, **Then** the harness streams progress notifications and the final result over the same session without the client retrying.
4. **Given** the operator opts the HTTP listener OUT (default), **When** the harness starts, **Then** the HTTP listener does NOT bind any port and stdio remains the only active surface.

---

### User Story 3 — External clients access bundle-published prompts and resources (Priority: P1)

The harness exposes prompts and resources defined inside currently-resolved bundles to external MCP clients via the standard MCP `prompts/*` and `resources/*` methods. The set is exactly what the active bundle dependency graph provides — overlays included — and respects bundle-level scoping (private prompts not exposed by default).

**Why this priority**: Bundles are the unit of durable configuration; if external clients can only see a hand-rolled tool catalog, the bundle-as-distribution story breaks. With this, a bundle author ships a prompt/resource pack and any MCP-aware client can consume it through the harness.

**Independent Test**: A test bundle declares one prompt and two resources. The harness loads the bundle, an external MCP client lists prompts and resources, and confirms each returned item matches the bundle's declarations.

**Acceptance Scenarios**:

1. **Given** the active bundle set declares a prompt `summarize_pr`, **When** the client calls `prompts/list`, **Then** `summarize_pr` appears with its argument schema and description.
2. **Given** the active bundle set declares a resource `internal_glossary.md`, **When** the client calls `resources/read` with the resource URI, **Then** the bytes + MIME type are returned.
3. **Given** a bundle declares a prompt as `private: true`, **When** the client lists prompts, **Then** the prompt does NOT appear unless the operator has explicitly enabled private prompt sharing.
4. **Given** the active bundle set is reloaded, **When** the bundle resolution completes, **Then** subsequent `prompts/list` and `resources/list` calls return the new union; existing in-flight calls finish under the old set.

---

### User Story 4 — Every external MCP interaction is auditable (Priority: P1)

Every `initialize`, every `tools/call`, every `prompts/get`, every `resources/read`, every notification, and every error becomes an append-only entry in the harness event log with credentials redacted. An operator can audit "which external client called what tool when" and replay the resulting harness state changes.

**Why this priority**: Exposing the harness as an MCP server is a privilege escalation surface — any MCP client connecting in is doing things to the harness. Without an audit trail, the harness violates its SOC 2-readiness posture (charter Policy Summary).

**Independent Test**: An external client runs a multi-call session, then the operator queries the event log and confirms it can reconstruct the full conversation, including which client (per session id) made which call.

**Acceptance Scenarios**:

1. **Given** an external client opens an MCP session and invokes one tool, **When** the operator reads the event log, **Then** entries exist for: connection accepted, handshake, tool call, tool result, session close — each with the session id and client info.
2. **Given** a tool's result includes data that matches a credential pattern, **When** the entry is written, **Then** the matching substring is redacted before persistence (defense-in-depth: tool results SHOULD NOT contain credentials in the first place).
3. **Given** an external client repeatedly fails authentication on the streamable-HTTP transport, **When** the failures land, **Then** they are logged as `mcp.server/auth_denied` events with the source IP — feeding any rate-limit / lockout policy.

---

### User Story 5 — Sampling callbacks route through the harness LLM connector (Priority: P2)

When a tool implementation (or a server-side prompt step) needs an LLM completion, it issues a sampling callback. Because the harness IS the MCP server here, the sampling callback is consumed by the harness's own LLM connector under the bundle's resolved provider profile. Result is returned to the tool implementation and ultimately to the external client as part of its `tools/call` response.

**Why this priority**: Sampling closes the loop: a harness-side tool can be itself agentic without each tool re-implementing LLM glue. Without this, every tool that wants a model call has to ship its own provider integration.

**Independent Test**: A test harness-side tool implementation issues a sampling call. The harness LLM connector returns a recorded fixture response. The tool returns the result through the MCP server to the external client.

**Acceptance Scenarios**:

1. **Given** a harness-side tool needs a model completion, **When** it issues a sampling call, **Then** the sampling routes through the harness `core/llm` registry under the bundle's selected provider profile.
2. **Given** the bundle has no LLM provider profile, **When** a sampling call is attempted, **Then** the tool implementation receives a typed `ErrSamplingUnavailable` and the external client's tool call returns a structured error.
3. **Given** the policy engine denies the model selection (charter Policy Summary), **When** the sampling routes, **Then** the call is denied with a typed `ErrPolicyDenied` and never reaches the LLM connector.

---

### User Story 6 — A new transport is added without modifying core (Priority: P3)

A contributor adds support for a new MCP server-side transport (e.g., websockets, named pipes, an enterprise-only mTLS scheme) by implementing the transport contract and registering it. No changes are required to the core packages outside the transport's own package.

**Why this priority**: Same reasoning as the MCP client mission's User Story 6 — protocol admits transport plurality and the open-source / enterprise distribution split must not depend on forking core.

**Independent Test**: A throwaway "in-memory" server transport is implemented in a separate package and registered. An MCP client opens an in-memory session against it; tools list and tool calls succeed — without any commit touching `core/mcp/server/` interface package or any other `core/` package.

**Acceptance Scenarios**:

1. **Given** a new transport implements the transport contract and registers itself, **When** the operator enables it, **Then** the harness starts listening through it using the same handler dispatch.
2. **Given** an attempt to add a new transport that requires modifying `core/mcp/server` interface or any other `core/` package, **When** the change is reviewed, **Then** the review flags the architectural-integrity violation before merge.

---

### Edge Cases

- An external client sends a malformed JSON-RPC frame: the harness responds with `ParseError` per JSON-RPC and continues serving the session; one bad frame must not abort the connection.
- An external client sends a `tools/call` for a tool the active bundle no longer exposes (race with bundle reload): the harness returns `MethodNotFound` with the tool name; the client may retry against the new tool list.
- A bundle reload removes a prompt while an external client is mid-`prompts/get`: the in-flight call completes against the resolved-at-call snapshot; subsequent lists reflect the new bundle state.
- An external client connects via stdio and never sends `initialize`: the harness times out the handshake within `mcp.server.handshake_timeout` (default 30 s) and closes stdout / exits the process gracefully.
- The harness is launched in stdio mode but stdout is also being used for log output: the harness MUST refuse to start unless logs are routed to stderr or a file; any stdout corruption breaks the protocol.
- Two external clients both open streamable-HTTP sessions and both invoke the same harness-side tool that mutates state (`run_bundle`): the harness serializes mutations through the appropriate subsystem (sessions / scheduler) per its existing concurrency contract; MCP does not relax those rules.
- An external client requests `roots/list` to discover the harness's filesystem roots: the harness exposes ONLY the bundle-declared roots, never `dataDir` or arbitrary host filesystem; missing roots return an empty list, never an error.
- The streamable-HTTP listener receives a request with an `Origin` header from a non-allowed origin (`example.com` when only `localhost` is allowed): the request is rejected with HTTP 403 and the rejection logged as `mcp.server/origin_denied`.

---

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001** — Day-one transports: stdio (the harness as a child process) and streamable-HTTP. Older HTTP+SSE transport NOT exposed server-side in v1 (the client mission keeps it for outbound compatibility; inbound surfaces converge on streamable-HTTP).
- **FR-002** — Top-level configuration `mcp.server.{stdio,http}` controls whether each transport is enabled. Stdio requires being launched as a child process (CLI flag); HTTP requires explicit `enabled: true` opt-in.
- **FR-003** — `initialize` / `initialized` handshake with capability + protocol version negotiation. The harness advertises tool / prompt / resource / sampling / roots capabilities based on what the active bundle set and configuration support.
- **FR-004** — `tools/list` and `tools/call`: harness-native tools (built-in) plus bundle-contributed tools (when bundle artifact `kind: mcp_tool` is declared — design accommodates but does not require day-one).
- **FR-005** — `prompts/list` and `prompts/get`: served from active bundle prompts. `private: true` prompts excluded by default.
- **FR-006** — `resources/list`, `resources/read`, `resources/templates/list`: served from active bundle resources.
- **FR-007** — `roots/list` (server side): exposes ONLY operator-declared bundle root paths; never `$HOME` / `$dataDir` / `/`.
- **FR-008** — Server-initiated logging notifications routed to harness event log (and re-emitted to subscribed clients via `notifications/message` on each session).
- **FR-009** — Sampling: when a tool implementation requests sampling, the harness routes it through the in-tree `core/llm` registry under the bundle's resolved provider profile.
- **FR-010** — Cancellation: a client-side `notifications/cancelled` for an in-flight `tools/call` interrupts the handler within 1 s p99.
- **FR-011** — Distinguish transient (network blips, internal subsystem temporary errors) from non-transient (`MethodNotFound`, `InvalidParams`, validation failures) via typed taxonomy mapped onto the JSON-RPC error envelope.
- **FR-012** — Pluggable transport contract: new transports added in their own package; no central registry edits.
- **FR-013** — Pluggable tool contract: new harness-native tools register as Go implementations of a `Tool` interface; bundles may also register tools via the `mcp_tool` artifact kind (design accommodates).
- **FR-014** — Audit emit: every handshake, request, response, notification, error, and lifecycle transition lands in the event log under the `mcp.server/` kind namespace.
- **FR-015** — Replay-aware: the server records sufficient context with each tool call entry that an event-log replay can reconstruct the exact tool invocation (parameters, session id, client info).
- **FR-016** — Origin / allowlist policy on streamable-HTTP: bind interface, allowed origins, optional bearer token gating; defaults: bind `127.0.0.1`, allow only `null` / `localhost` origins, no auth required for loopback.
- **FR-017** — Authentication on streamable-HTTP: optional bearer-token auth referencing a `core/secrets` credential. When configured, requests without a valid bearer return HTTP 401.
- **FR-018** — Static curated tool set in v1: `run_bundle`, `list_bundles`, `query_event_log` (read-only audit access), `list_sessions`, `list_provider_profiles`. Each tool's argument schema is published; results are JSON-encoded.
- **FR-019** — Lifecycle: `Start(ctx)`, `Shutdown(ctx)` on the server façade; integrate with `core.Core.Start` / `core.Core.Shutdown`.
- **FR-020** — Graceful drain on shutdown: in-flight tool calls complete (within a configurable max drain timeout, default 30 s) before the listener closes.

### Non-Functional Requirements

- **NFR-001** — Handshake latency (stdio): < 50 ms p95 from launch to `initialize` response.
- **NFR-002** — Handshake latency (streamable-HTTP loopback): < 100 ms p95.
- **NFR-003** — Tool dispatch overhead (server side): < 10 ms p95 above raw subsystem latency.
- **NFR-004** — Cancellation responsiveness: < 1 s p99 from `notifications/cancelled` to handler exit.
- **NFR-005** — Memory footprint: < 32 MB resident additional when MCP server is enabled in stdio mode (excluding the rest of the harness).
- **NFR-006** — No plaintext credentials in any event log entry.
- **NFR-007** — Redaction recall ≥ 99 % across known credential patterns at v1.
- **NFR-008** — Append-only invariant: log entries written by the MCP server are never edited or deleted in place.
- **NFR-009** — Stdio transport MUST work with zero network access.
- **NFR-010** — Streamable-HTTP listener MUST refuse to bind to a non-loopback interface unless the operator opts in via configuration.
- **NFR-011** — Architectural-integrity: nothing outside `core/mcp/server/<transport>/` imports a transport-specific library.
- **NFR-012** — JSON-RPC framing strictness: stdio mode produces ONLY JSON-RPC frames on stdout; any internal logging routes to stderr or the event log.

### Constraints

- **C-001** — `core/mcp/server/` is the only seam through which the harness exposes inbound MCP. Wails / RPC / UI never expose MCP directly.
- **C-002** — Bundle artifacts of `kind: mcp_tool` for bundle-contributed tools may be designed but day-one tool catalog is harness-native only.
- **C-003** — All audit goes through the harness event log; the MCP server does NOT own a parallel log file.
- **C-004** — Authentication credentials referenced indirectly via `core/secrets` (no plaintext bearer tokens in config).
- **C-005** — Two-track distribution: open-source core ships stdio + streamable-HTTP; enterprise-only auth schemes (mTLS, SSO bridge) live in build-tagged packages without forking core.
- **C-006** — Local-first: stdio MUST work with zero network access; the streamable-HTTP listener defaults to OFF and to loopback-only when ON.
- **C-007** — Sampling callbacks are gated by the policy engine (charter Policy Summary); a sampling request that fails policy is denied with a typed protocol error.
- **C-008** — Architectural-integrity boundary (DIRECTIVE_001): `core/mcp/server/` may import `core/llm`, `core/event`, `core/secrets`, `core/bundle`, `core/session`, `core/scheduler` (all in-tree contracts); it does NOT import Wails or frontend types.

### Key Entities

- **Server Configuration** — top-level config block `mcp.server.{stdio,http}` controlling listener lifecycle.
- **Session** — one live MCP client connection (one stdio process attachment OR one streamable-HTTP session id); owns the JSON-RPC routing for that client.
- **Tool Registration** — Go implementation of a harness-native tool with argument schema + result builder.
- **Bundle Tool Artifact** — bundle artifact `kind: mcp_tool` (design-accommodates; not day-one populated).
- **Roots Set** — operator-declared filesystem path scopes the harness exposes via `roots/list`.
- **Sampling Request** — internal call from a tool implementation to the harness LLM connector, returning to the tool implementation.

---

## Assumptions

1. The MCP protocol version target is 2025-06-18 + streamable-HTTP transport additions; the server negotiates down on connect.
2. The harness has the `mcp serve` CLI subcommand entry point for stdio mode (added during this mission's implementation).
3. The `core/llm`, `core/secrets`, `core/event`, `core/bundle`, `core/session`, and `core/scheduler` subsystems are wired and accessible.
4. Day-one harness-native tools are read-mostly (`query_event_log`, `list_*`) plus one mutating tool (`run_bundle`); future tools added incrementally.
5. The harness does NOT run as a privileged service; all tool calls execute under the harness process's UID.

## Open Questions

1. **OQ-1 — Bundle-contributed tool artifact kind** — Do we ship `mcp_tool` as a registered bundle artifact kind in v1 with at least one example, or defer to v1.x and only ship harness-native tools? Default if unresolved: **defer the artifact kind contract to a follow-up mission**, ship the harness-native catalog day-one.
2. **OQ-2 — Streamable-HTTP authentication default** — When the operator opts the HTTP listener in but does not configure auth, do we (a) default to no-auth on loopback, (b) require a bearer token always, or (c) emit an explicit warning? Default if unresolved: **(a) loopback-only no-auth allowed; non-loopback requires explicit bearer**.
3. **OQ-3 — Sampling profile selection** — When a tool issues a sampling request without a model hint, which provider profile do we choose? Default if unresolved: **the bundle's `default_provider` field; if absent, the first registered provider profile**.
4. **OQ-4 — Resource size cap** — What's the upper bound on `resources/read` payload size before we surface a typed `ResourceTooLarge` error? Default if unresolved: **8 MiB; configurable**.

## Success Criteria

- **SC-001** — Stdio mode passes a black-box conformance run against the official MCP test client (or our recorded equivalent) for: handshake, tools/list, tools/call, prompts/list, prompts/get, resources/list, resources/read, sampling callback.
- **SC-002** — Streamable-HTTP mode passes the same matrix on loopback with both no-auth and bearer-auth configurations.
- **SC-003** — Audit suite: zero plaintext credentials across the full transport matrix; redaction tests cover the same patterns as the MCP client mission.
- **SC-004** — Replay determinism: a recorded inbound session replays without re-executing tool side-effects (replay layer treats MCP server entries as observations, not invocations).
- **SC-005** — Charter ≥ 80 % line coverage on `core/mcp/server/**` per testing standards.
- **SC-006** — Refuses to start in stdio mode if stdout is unsuitable (test against a fake stdout that buffers / colorizes).
