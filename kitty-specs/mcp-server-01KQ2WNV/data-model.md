# Data Model: MCP Server

**Mission**: `mcp-server-01KQ2WNV`
**Date**: 2026-04-25

This document captures the data shapes the MCP server mission introduces.
Public Go signatures sit in plan §3; this file traces the on-disk and
on-the-wire shapes.

---

## 1. Top-level configuration: `mcp.server.*`

The MCP server's lifecycle is controlled by harness configuration
(NOT a bundle artifact — exposing inbound MCP is an operator-level
decision, not a bundle author's). Stored in the harness config file
(`config.yaml` per existing `core/config` conventions).

```yaml
mcp:
  server:
    stdio:
      enabled: false                # set via CLI flag, not config
      handshake_timeout_ms: 30000
    http:
      enabled: false                # default OFF
      bind: "127.0.0.1:0"           # loopback by default
      allowed_origins:              # CORS / origin allowlist
        - "null"
        - "localhost"
        - "127.0.0.1"
      auth:                         # optional bearer token
        bearer_token:
          keychain: kaneaz/mcp-server-bearer
      drain_timeout_ms: 30000
      handshake_timeout_ms: 30000
    limits:
      max_request_bytes: 8388608    # 8 MiB
      max_response_bytes: 8388608
      max_concurrent_sessions: 16
    tools:
      run_bundle:
        enabled: true
      query_event_log:
        enabled: true
      list_bundles:
        enabled: true
      list_sessions:
        enabled: true
      list_provider_profiles:
        enabled: true
    roots:                          # explicit operator-declared
      - /home/operator/projects
      - /home/operator/workspace
    sampling:
      max_depth: 4
```

Validation rules (`core/mcp/server/config_schema.go`):

- `mcp.server.stdio.enabled` may only be `true` when launched with
  the `mcp serve --stdio` CLI flag.
- `mcp.server.http.bind` MUST be `127.0.0.1:*` or `::1:*` unless the
  operator explicitly opts the listener to a non-loopback bind via
  `mcp.server.http.allow_non_loopback: true` (a separate explicit flag).
- `mcp.server.http.auth.bearer_token` MUST be a credential reference
  (no inline plaintext) when present.
- `roots` paths MUST be absolute.

---

## 2. JSON-RPC wire shapes

Reused from MCP-client mission's `core/mcp/client/jsonrpc/` package
(or refactored to `core/mcp/jsonrpc/` for symmetry — see plan §2). The
shapes are identical; only the directionality differs.

The server's JSON-RPC handler dispatches by method name to per-method
Go handler functions:

```go
type Handler interface {
    Method() string
    Handle(ctx context.Context, sess *Session, params json.RawMessage) (json.RawMessage, *RPCError)
}
```

Handler registration happens at server start; the dispatch table is
built once.

---

## 3. Public types (re-stated from plan §3)

These materialize in `core/mcp/server/server.go`:

```go
// Server is the inbound MCP server façade.
type Server interface {
    Start(ctx context.Context) error
    Shutdown(ctx context.Context) error
    Sessions() []SessionSnapshot
    RegisterTransport(kind string, factory TransportFactory)
    RegisterTool(t Tool)
}

// Session is the per-client session handle exposed to tool handlers.
type Session interface {
    ID() string
    ClientInfo() ClientInfo
    Sample(ctx context.Context, req SamplingRequest) (SamplingResponse, error)
    NotifyProgress(token string, p Progress)
    Logf(level LogLevel, format string, args ...any)
}

// Tool is the harness-native tool contract.
type Tool interface {
    Name() string
    Description() string
    InputSchema() json.RawMessage
    Call(ctx context.Context, args json.RawMessage, sess Session) (ToolResult, error)
}

type ToolResult struct {
    Content    []ContentBlock
    IsError    bool
    Structured json.RawMessage
}

type ContentBlock struct {
    Type     string
    Text     string
    Data     []byte
    MIMEType string
    URI      string
}

type ClientInfo struct {
    Name    string
    Version string
}

type SessionSnapshot struct {
    ID         string
    Transport  string
    OpenedAt   time.Time
    ClientInfo ClientInfo
    Source     string // remote address for HTTP, "stdio" for stdio
}
```

---

## 4. Event-log kinds emitted by the server

Namespaced `mcp.server/` per upstream `event-log` FR-017. Each carries
`session_id`, `emitter_id="mcp/server"`, `event_id` (ULID), and a
redacted payload.

| Kind | Purpose | FR coverage |
|---|---|---|
| `mcp.server/listener_started` | Listener bound (per transport) | FR-014, FR-019 |
| `mcp.server/listener_stopped` | Listener stopped | FR-014, FR-019 |
| `mcp.server/session_opened` | Inbound session opened | FR-014 |
| `mcp.server/session_closed` | Inbound session closed | FR-014 |
| `mcp.server/handshake` | `initialize` exchange completed | FR-003 |
| `mcp.server/handshake_failed` | Handshake error / timeout | FR-003 |
| `mcp.server/origin_denied` | HTTP origin rejected | FR-016 |
| `mcp.server/auth_denied` | HTTP bearer-token check failed | FR-017 |
| `mcp.server/tool_call` | `tools/call` invocation | FR-004, FR-014 |
| `mcp.server/tool_result` | tool returned | FR-004, FR-014 |
| `mcp.server/tool_error` | tool failed | FR-014 |
| `mcp.server/prompt_get` | `prompts/get` invocation | FR-005 |
| `mcp.server/resource_read` | `resources/read` invocation | FR-006 |
| `mcp.server/sampling_issued` | server → client sampling request | FR-009 |
| `mcp.server/sampling_completed` | sampling result returned | FR-009 |
| `mcp.server/notification_sent` | server-pushed notification | FR-008 |
| `mcp.server/protocol_warning` | unknown method or malformed frame | FR-011 |
| `mcp.server/cancelled` | client-side `notifications/cancelled` | FR-010 |
| `mcp.server/error` | non-transient error surfaced | FR-011 |
| `mcp.server/policy_denied` | sampling / tool denied by policy | C-007 |

---

## 5. Tool argument and result shapes

Each harness-native tool defines its argument schema as JSON Schema and
its result content blocks. Schemas live alongside the implementation:

### `run_bundle`

```yaml
input_schema:
  type: object
  properties:
    bundle_id:
      type: string
      description: "Resolved bundle id"
    args:
      type: object
      description: "Bundle-specific arguments (passed through verbatim)"
  required: [bundle_id]
result:
  - type: text  # session id of the started session
```

### `query_event_log`

```yaml
input_schema:
  type: object
  properties:
    session_id: { type: string }
    kind_prefix: { type: string }  # e.g. "llm/" or "mcp.server/"
    since: { type: string, format: date-time }
    limit: { type: integer, default: 100, maximum: 1000 }
result:
  - type: text  # JSON-encoded array of redacted events
```

### `list_bundles`, `list_sessions`, `list_provider_profiles`

Each returns a JSON-encoded array of structured info, as a `text`
content block.

---

## 6. Roots data shape

Roots returned via `roots/list` are simple URI shapes:

```json
{
  "roots": [
    {"uri": "file:///home/operator/projects", "name": "projects"},
    {"uri": "file:///home/operator/workspace", "name": "workspace"}
  ]
}
```

Sourced ONLY from `mcp.server.roots:` config (FR-007 — never from
`dataDir` or arbitrary host filesystem).

---

## 7. Authentication wire shapes

Bearer-token check (when `mcp.server.http.auth.bearer_token` is
configured):

- Request `Authorization: Bearer <token>` is required.
- The harness resolves the configured cred ref to obtain the expected
  token at server start (cached for the listener's lifetime).
- Comparison is constant-time (`subtle.ConstantTimeCompare`).
- On mismatch / absence: HTTP 401, body `{"error":"unauthorized"}`,
  emit `mcp.server/auth_denied` with the source IP and the configured
  ref's `Kind+Locator` (NEVER the resolved token).

---

## 8. Internal session state

Per-session state lives in memory only. State is not persisted across
process restarts — sessions are torn down on shutdown.

```go
type session struct {
    id              string
    transport       string                // "stdio" | "streamable_http"
    transportImpl   ServerTransport        // owns the transport-specific I/O
    state           sessionState
    serverInfo      implementationInfo
    clientInfo      implementationInfo
    capabilities    sessionCapabilities
    openedAt        time.Time
    source          string                  // remote addr for HTTP
    pendingMu       sync.Mutex
    pending         map[string]chan response // server → client outbound requests
    audit           AuditEmitter
    cancel          context.CancelFunc
    samplingDepth   int
}
```

The transport interface (FR-012 pluggable):

```go
type ServerTransport interface {
    Send(ctx context.Context, frame []byte) error
    Recv(ctx context.Context) ([]byte, error)
    Close() error
    Source() string  // "stdio" or "ip:port"
}
```

---

## 9. Bundle artifact: deferred `kind: mcp_tool`

Per spec OQ-1, bundle-contributed tools via a registered `mcp_tool`
artifact kind are deferred to a follow-up mission. The plan
accommodates the artifact-kind handler shape but ships an empty handler
day-one (no bundle-contributed tools).

When the follow-up mission lands:

```yaml
# Anticipated shape
artifacts:
  - kind: mcp_tool
    name: deploy
    path: mcp-tools/deploy.yaml

# mcp-tools/deploy.yaml
schema_version: 1
name: deploy
description: "Deploy the active branch to staging"
input_schema:
  type: object
  properties:
    target: { type: string }
implementation:
  kind: command
  command: ["./scripts/deploy"]
  args_template: ["{{.target}}"]
```

This is design-accommodation only; not implemented in this mission.
