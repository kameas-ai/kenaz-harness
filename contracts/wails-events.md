# Wails event topic registry

This file is the canonical registry of every Wails event topic the harness
emits. Adding a new topic requires editing this file in the same PR
(DIRECTIVE_010, plan §5.4 / WP11). Privacy CI invariant
(`scripts/ci/check-emitter-isolation.sh`) blocks any `runtime.EventsEmit`
call outside `core/rpc/emitter.go` and `core/rpc/stream_broker.go`, so the
broker is the sole gatekeeper.

## Topic naming

`<view-name>:<event-kind>` — both names lowercase, underscore-free.
`<event-kind>` is one of:

- `event` — domain payload routed through `streamBroker.Subscribe`.
- `stream-closed` — emitted automatically by the broker on close;
  payload `{ id, reason, message? }` with `reason ∈ {ctx-cancelled,
  stop-called, backend-error}` (see plan §4.2).

## v1.0 topics (26 entries)

| Topic                    | Payload (Go)                | Payload (TS)              | Cardinality        | Lifecycle                 | Ordering                | Privacy                                   |
|--------------------------|-----------------------------|---------------------------|--------------------|---------------------------|-------------------------|-------------------------------------------|
| `sessions:event`         | `sessions.SessionEvent`     | `SessionEvent`            | 1 per source item  | until source closes       | source order preserved  | redacted server-side per event-log mission |
| `sessions:stream-closed` | `StreamClosedPayload`       | `StreamClosedPayload`     | 1 per close        | once                      | n/a                     | reason + message only                     |
| `mcp:event`              | `mcp.ServerEvent`           | `MCPEvent`                | 1 per source item  | until source closes       | source order preserved  | redacted                                  |
| `mcp:stream-closed`      | `StreamClosedPayload`       | `StreamClosedPayload`     | 1 per close        | once                      | n/a                     | reason + message only                     |
| `a2a:event`              | `a2a.CardEvent`             | `A2AEvent`                | 1 per source item  | until source closes       | source order preserved  | redacted                                  |
| `a2a:stream-closed`      | `StreamClosedPayload`       | `StreamClosedPayload`     | 1 per close        | once                      | n/a                     | reason + message only                     |
| `llm:event`              | `llm.ProviderEvent`         | `LLMEvent`                | 1 per source item  | until source closes       | source order preserved  | redacted; never carries credentials       |
| `llm:stream-chunk`       | `llmview.StreamChunkPayload`| `LLMStreamChunkPayload`   | 1 per generation chunk | until generation ends | generation order preserved | structural redaction (no credentials, no raw HTTP frames); event-log redactor catches accidental matches |
| `llm:stream-closed`      | `llmview.StreamClosedPayload` \| `StreamClosedPayload` | `LLMStreamClosedPayload`     | 1 per close        | once                      | n/a                     | reason + message + finish_reason only     |
| `policy:event`           | `policy.DecisionEvent`      | `PolicyDecisionEvent`     | 1 per decision     | until source closes       | source order preserved  | clause + violating-input only             |
| `policy:stream-closed`   | `StreamClosedPayload`       | `StreamClosedPayload`     | 1 per close        | once                      | n/a                     | reason + message only                     |
| `audit:event`            | `audit.Entry`               | `AuditEntry`              | 1 per entry        | until source closes       | append-only             | redacted server-side                      |
| `audit:stream-closed`    | `StreamClosedPayload`       | `StreamClosedPayload`     | 1 per close        | once                      | n/a                     | reason + message only                     |
| `workflow:event`         | `workflow.JobEvent`         | `WorkflowEvent`           | 1 per source item  | until source closes       | source order preserved  | redacted                                  |
| `workflow:stream-closed` | `StreamClosedPayload`       | `StreamClosedPayload`     | 1 per close        | once                      | n/a                     | reason + message only                     |
| `bundle:event`           | `bundle.BundleEvent`        | `BundleEvent`             | 1 per source item  | until source closes       | source order preserved  | redacted                                  |
| `bundle:stream-closed`   | `StreamClosedPayload`       | `StreamClosedPayload`     | 1 per close        | once                      | n/a                     | reason + message only                     |
| `context:event`          | `contextview.ContextEvent`  | `ContextEvent`            | 1 per source item  | until source closes       | source order preserved  | redacted                                  |
| `context:stream-closed`  | `StreamClosedPayload`       | `StreamClosedPayload`     | 1 per close        | once                      | n/a                     | reason + message only                     |
| `secrets:event`          | `secrets.ReferenceEvent`    | `SecretsEvent`            | 1 per source item  | until source closes       | source order preserved  | reference-only metadata; NEVER values     |
| `secrets:stream-closed`  | `StreamClosedPayload`       | `StreamClosedPayload`     | 1 per close        | once                      | n/a                     | reason + message only                     |
| `storage:event`          | `storage.Event`             | `StorageEvent`            | 1 per source item  | until source closes       | source order preserved  | redacted                                  |
| `storage:stream-closed`  | `StreamClosedPayload`       | `StreamClosedPayload`     | 1 per close        | once                      | n/a                     | reason + message only                     |
| `scheduler:event`        | `scheduler.TickEvent`       | `SchedulerEvent`          | 1 per tick         | until scheduler stops     | tick order preserved    | redacted                                  |
| `scheduler:stream-closed`| `StreamClosedPayload`       | `StreamClosedPayload`     | 1 per close        | once                      | n/a                     | reason + message only                     |

## `llm:stream-chunk` payload shape

The LLM connector pumps generation chunks through the broker rather
than the channel-driven Subscribe path so per-generation goroutine
count stays at one. The payload mirrors `core/rpc/views/llm.StreamChunkPayload`:

```json
{
  "sub_id": "llm-7",
  "chunk": {
    "kind": "text",                 // text | tool | reasoning | usage | finish | error
    "text": "Hello",                // present on text chunks
    "tool": { ... },                // present on tool chunks (ToolUse)
    "reasoning": { ... },           // present on reasoning chunks
    "usage": { ... },               // present on usage chunks
    "finish": "end_turn",           // present on finish chunks
    "err": "..."                    // present on error chunks
  }
}
```

## `llm:stream-closed` payload shape

```json
{
  "sub_id": "llm-7",
  "reason": "completed",            // completed | stop-called | backend-error
  "message": "...",                 // present on backend-error
  "finish_reason": "end_turn"       // present on completed
}
```

Note: this payload differs from the broker's generic `StreamClosedPayload`
because the LLM connector pumps directly (not via Subscribe). Other
view-scoped `<view>:stream-closed` topics still carry the broker shape
`{id, reason, message?}`.

## v1.x reservations

- `shell:status-changed` — push event replaces the 5 s `useShellStatus`
  poll once cross-cutting status pushes through the broker (plan §7 v1.x).

## Adding a topic

1. Add the topic row to the table above.
2. Define the Go-side payload struct in the corresponding `core/rpc/views/<view>` package.
3. Mirror the TS interface in `frontend/src/lib/types.ts`.
4. The Go-side caller must use `streamBroker.Subscribe(ctx, view, kind, source)` —
   never `runtime.EventsEmit` directly. CI enforces.
