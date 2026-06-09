# Harness audit-event line-protocol

**Status:** Phase A (audit sink). **Owner:** `golang`.
**Producer:** the in-VM harness service (`kenaz-harness/cmd/harness-vm/`,
`auditsink.go`). **Consumer:** the reporter's collector audit source.

The harness writes **line-delimited JSON** records — one object per line — to
the Unix socket named by the environment variable `KENAZ_HARNESS_EVENT_SOCK`.
Each record describes one task lifecycle transition. The reporter's collector
source reads these lines, maps `kind` onto a new `event.Kind`, and maps the
remaining fields onto event metadata.

This is a **side channel** distinct from the RPC stream of
[`vm-rpc.md`](./vm-rpc.md): the RPC stream (`task.running` chunks) carries live
task *content* to the connected orchestrator; the audit line-protocol carries
**metadata only** to the durable audit timeline.

---

## Transport

| Property      | Value |
|---------------|-------|
| Endpoint      | Unix stream socket named by `KENAZ_HARNESS_EVENT_SOCK`. |
| Framing       | Line-delimited JSON (`\n` after each object). |
| Direction     | Harness → reporter collector (write-only from the harness side). |
| When unset    | Audit emission is **disabled**: the sink is a no-op and the task surface runs unchanged (dev / host mode). |
| Availability  | **Best-effort, non-blocking.** Each record is written on a short-lived connection with bounded dial + write deadlines (500 ms each). If the socket is absent, slow, or the write fails, the record is dropped silently — emission MUST NEVER block or fail a task. |

---

## Record shape

```json
{
  "kind":        "task.start" | "task.tool_call" | "task.tool_result" | "task.complete",
  "ts":          1717900000123,
  "task_id":     "task-abc",
  "tool":        "plan",
  "node":        "transform",
  "exit_code":   0,
  "duration_ms": 25
}
```

| Field         | Type   | Meaning |
|---------------|--------|---------|
| `kind`        | string | Lifecycle transition. One of the four kinds below. |
| `ts`          | int    | Emission time, Unix **milliseconds**. |
| `task_id`     | string | The task this record belongs to. |
| `tool`        | string | Tool / graph-node **name** (structural id). Empty for `task.start` and `task.complete`. |
| `node`        | string | Graph node **kind** (structural id, e.g. `transform`, `llm`, `tool`). Empty for `task.start` and `task.complete`. |
| `exit_code`   | int    | `0` = ok; non-zero = failure/cancel. Set on `task.tool_result` and `task.complete`; `0` otherwise. |
| `duration_ms` | int    | Wall-clock duration in milliseconds. Set on `task.tool_result` (node fire) and `task.complete` (whole task); `0` otherwise. |

### Kinds

| `kind`              | Emitted when                                   | Populated fields |
|---------------------|------------------------------------------------|------------------|
| `task.start`        | A task is dispatched (`task.start` RPC).        | `kind`, `ts`, `task_id` |
| `task.tool_call`    | A graph node / tool invocation **begins**.      | `+ tool`, `node` |
| `task.tool_result`  | A graph node / tool invocation **completes**.   | `+ tool`, `node`, `exit_code`, `duration_ms` |
| `task.complete`     | The task reaches a terminal state.              | `+ exit_code`, `duration_ms` |

**Ordering invariant.** Within a task: `task.start` is first and
`task.complete` is last; every `task.tool_call` is followed by a matching
`task.tool_result` (call-before-result, balanced). A cancelled run still closes
out each started node with a `task.tool_result` so the timeline never carries a
dangling call.

---

## Reporter mapping (consumer side)

The reporter's collector source maps each line onto an `event`:

- `kind` → the new `event.Kind` (`task.start` / `task.tool_call` /
  `task.tool_result` / `task.complete`).
- `tool` (falling back to `node`) → `Subject`.
- `exit_code` + `duration_ms` → `SizeChip` (e.g. `exit 0 · 25ms`).
- `ts` → event timestamp; `task_id` → correlation key.

---

## Privacy invariant (HARD GATE)

The audit line-protocol is **METADATA-ONLY**. A record carries **NO prompt
text, NO tool arguments, and NO tool output bodies — ever.**

This is enforced **structurally, not by convention**: the producer's
`auditRecord` type has no field that can hold user-authored content. The only
string-typed fields are `tool` (a tool *name*) and `node` (a graph node
*kind*) — both structural identifiers derived from the graph topology, never
from prompt or I/O content. There is no code path that can place a prompt,
argument, or output body onto the wire.

The producer mirrors the harness OTEL discipline
(`core/tasks/telemetry.go`): audit lines are emitted **alongside** the
per-node OTEL spans, at the same hook points, with the same metadata-only
attribute set (the kernel's `TraceSink` seam is passed only `node_id` /
`run_id`, never content).

**Consumer obligation:** the collector source and any downstream serializer
MUST **fail closed** — drop the record, never populate a content field — if a
record ever appears to carry a content field. The invariant holds on both
sides of the socket.
