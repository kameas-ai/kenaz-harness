# Data Model: MCP Client

**Mission**: `mcp-client-01KQ2WHG`
**Date**: 2026-04-25

This document captures the data shapes the MCP client mission introduces.
Public Go signatures sit in plan §3; this file traces the on-disk and
on-the-wire shapes.

---

## 1. Bundle artifact: MCP Server declaration

Bundle artifact `kind: mcp_server`. Stored as YAML inside the bundle.

```yaml
# In a bundle's manifest declares one or more artifacts of kind: mcp_server
artifacts:
  - kind: mcp_server
    name: filesystem-tools
    path: mcp/filesystem-tools.yaml
    content_hash: sha256:...
    kind_metadata: { schema_version: 1 }

# mcp/filesystem-tools.yaml
schema_version: 1
id: filesystem-tools
transport: stdio                 # "stdio" | "http_sse" | "streamable_http"
command:                         # stdio only
  - "/usr/local/bin/mcp-filesystem"
  - "--root"
  - "/data"
env:                             # stdio only; values may be credential refs
  HOME: "${env:HOME}"
url: ""                          # http transports only
headers:                         # http transports only
  Authorization:
    keychain: kaneaz/filesystem-mcp-token
roots:                           # filesystem scopes the server may operate on
  - /data/projects
retry:
  max_attempts: 3
  base_ms: 250
  max_ms: 5000
  jitter: full
limits:
  max_result_bytes: 8388608      # 8 MiB
  handshake_timeout_ms: 30000
```

### Validation rules (`core/mcp/client/spec_schema.go`)

- `transport` ∈ {`stdio`, `http_sse`, `streamable_http`, …registered}.
- `command` REQUIRED when `transport == stdio`; `url` REQUIRED otherwise.
- `headers` values may be either string literals (no plaintext credential
  patterns — defense-in-depth scan) OR credential references following the
  `core/secrets` reference shape.
- `roots` paths MUST be absolute and MUST NOT escape the bundle's declared
  data scope.
- `id` unique within the harness's resolved server set (resolver `FR-009`
  conflict-detection path).

### Registration flow

1. `bundle-format-resolver` discovers artifacts of `kind: mcp_server`.
2. Calls into the MCP client's `ArtifactKindHandler` (registered at
   process start).
3. Handler validates, parses, and registers the resulting `ServerSpec`
   with `core/mcp/client.Pool`.
4. Resolution + validation events emitted to event log.

---

## 2. JSON-RPC wire shapes

The client speaks JSON-RPC 2.0 over the chosen transport. Internal Go
types live under `core/mcp/client/jsonrpc/` and never escape the package
boundary — the public Pool API speaks `core/mcp/client.Tool`,
`core/mcp/client.Prompt`, etc.

```go
// jsonrpc/frame.go (internal)
type request struct {
    Version string          `json:"jsonrpc"` // always "2.0"
    ID      json.RawMessage `json:"id,omitempty"`
    Method  string          `json:"method"`
    Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
    Version string          `json:"jsonrpc"`
    ID      json.RawMessage `json:"id"`
    Result  json.RawMessage `json:"result,omitempty"`
    Error   *rpcError       `json:"error,omitempty"`
}

type notification struct {
    Version string          `json:"jsonrpc"`
    Method  string          `json:"method"`
    Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
    Code    int             `json:"code"`
    Message string          `json:"message"`
    Data    json.RawMessage `json:"data,omitempty"`
}
```

### MCP-specific request/response payloads

```go
// initialize request params (client → server)
type initializeParams struct {
    ProtocolVersion string                 `json:"protocolVersion"`
    Capabilities    clientCapabilities     `json:"capabilities"`
    ClientInfo      implementationInfo     `json:"clientInfo"`
}

// initialize response result (server → client)
type initializeResult struct {
    ProtocolVersion string                 `json:"protocolVersion"`
    Capabilities    serverCapabilities     `json:"capabilities"`
    ServerInfo      implementationInfo     `json:"serverInfo"`
    Instructions    string                 `json:"instructions,omitempty"`
}
```

Tool / prompt / resource shapes follow the official MCP schema. They are
defined in `core/mcp/client/jsonrpc/methods.go` as struct types that
match the spec's JSON Schema.

---

## 3. Public types (re-stated from plan §3 for traceability)

These materialize as Go types in `core/mcp/client/client.go`:

```go
type Tool struct {
    Server      string          // server id
    Name        string          // tool name
    Description string
    InputSchema json.RawMessage // JSON Schema (raw)
}

type Prompt struct {
    Server      string
    Name        string
    Description string
    Arguments   []PromptArgument
}

type PromptArgument struct {
    Name        string
    Description string
    Required    bool
}

type Resource struct {
    Server   string
    URI      string
    Name     string
    MIMEType string
}

type ToolCallResult struct {
    Content    []ContentBlock
    IsError    bool
    StructuredOutput json.RawMessage // optional structured output
}

type ContentBlock struct {
    Type     string          // "text" | "image" | "resource"
    Text     string
    Data     []byte          // image / blob
    MIMEType string
    Resource *Resource       // when type == "resource"
}
```

---

## 4. Event-log kinds emitted by the client

Namespaced `mcp/` per upstream `event-log` FR-017. Each carries
`session_id`, `emitter_id="mcp/client"`, `server_id`, `event_id` (ULID),
and a redacted payload.

| Kind | Purpose | FR coverage |
|---|---|---|
| `mcp/pool_open` | Pool opened with N servers | FR-014, FR-015 |
| `mcp/pool_close` | Pool closing | FR-014 |
| `mcp/pool_reload` | Pool reload diff | FR-014 |
| `mcp/server_initialize` | Server handshake start | FR-004, FR-015 |
| `mcp/server_initialized` | Server handshake complete (capabilities) | FR-004 |
| `mcp/server_initialize_failed` | Handshake error | FR-004, FR-012 |
| `mcp/server_unhealthy` | Server marked unhealthy after retries exhausted | FR-013 |
| `mcp/tool_call_request` | `tools/call` issued | FR-005, FR-015 |
| `mcp/tool_call_response` | `tools/call` returned | FR-005 |
| `mcp/prompt_get` | `prompts/get` round-trip | FR-006 |
| `mcp/resource_read` | `resources/read` round-trip | FR-007 |
| `mcp/sampling_request` | server-initiated `sampling/createMessage` | FR-009 |
| `mcp/sampling_response` | sampling completion returned to server | FR-009 |
| `mcp/server_log` | `notifications/message` from server | FR-008 |
| `mcp/protocol_warning` | unknown method or unexpected frame | FR-012 |
| `mcp/transport_failure` | transient transport-level failure | FR-012, FR-013 |
| `mcp/retry_attempted` | retry attempt with backoff delay | FR-013 |
| `mcp/cancelled` | caller cancellation reached upstream | FR-011 |
| `mcp/error` | non-transient error surfaced to caller | FR-012 |
| `mcp/server_exit` | stdio child exited (with status) | FR-012 |

Per US3 Acceptance 1, a successful tool call produces at minimum
`pool_open` → `server_initialize` → `server_initialized` →
`tool_call_request` → `tool_call_response` → `pool_close`.

---

## 5. Internal session state

The Pool tracks per-server connection state. State is not persisted across
process restarts — pool open is always from-scratch (replay determinism is
the event-log layer's responsibility, not the pool's).

```go
type connectionState int
const (
    stateConnecting connectionState = iota
    stateInitializing
    stateReady
    stateUnhealthy
    stateClosing
    stateClosed
)

type connection struct {
    spec        ServerSpec
    transport   Transport               // implements Send / Recv
    state       connectionState
    serverInfo  implementationInfo
    capabilities serverCapabilities
    pendingMu   sync.Mutex
    pending     map[string]chan response // keyed by JSON-RPC id
    retry       retryPolicy
    audit       AuditEmitter
    cancel      context.CancelFunc
}
```

The transport interface (FR-017 pluggable) is:

```go
type Transport interface {
    Send(ctx context.Context, frame []byte) error
    Recv(ctx context.Context) ([]byte, error)
    Close() error
}
```

Each transport package implements this — `stdio.Transport`, `httpsse.Transport`,
`streamable.Transport`. The connection layer is transport-agnostic.

---

## 6. Cost / metrics surface

The MCP client does NOT track cost (the LLM connector owns that for
sampling callbacks) but it does track:

- per-server tool call counts
- per-server response sizes (bytes)
- per-server error counts by typed error class
- per-server retry counts

Exposed via `Pool.Metrics()` returning a snapshot. RPC surface (`MCP.Metrics`)
optional — useful for the Wails UI but not required for v1.
