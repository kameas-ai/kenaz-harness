# kenaz-harness-vm Wire-Format Contract (Phase 1 Draft)

**Status:** DRAFT (Phase 1)
**Owner:** Phase 1 (`golang`); Phase 8 supersedes the implementation
**Last updated:** 2026-05-16

This document defines the wire format between the host orchestrator
(`kenaz-workbench/internal/orchestrator/`) and the in-VM harness service
(`kenaz-harness/cmd/harness-vm/`). It is a Phase 1 draft — Phase 8 replaces
the stub implementation while preserving this framing so the upgrade is additive.

---

## Transport

### Phase 1 (stub)

- **Protocol:** TCP over Tart NAT (macOS) / loopback (WSL2).
- **Port:** 7881 (default); overridable via `HARNESS_VM_PORT` env var.
- **Connection model:** one task per connection. The client opens a TCP
  connection, sends one request, reads one response, and closes.

### Phase 8 (target)

- **Protocol:** vsock (`AF_VSOCK`).
  - macOS (Tart): vsock CID assigned by the hypervisor; host dials
    `{cid, port=7881}`. The Tart substrate driver exposes the CID via
    `tart get <vmname> --format json`.
  - Windows (WSL2): vsock via `AF_HYPERV` (Hyper-V socket).
    `VMADDR_CID_ANY` on the guest side; host uses the WSL2 vmId GUID.
- **Connection model:** long-lived per-workbench connection with
  multiplexed task streams. Phase 8 will define stream framing.

The port number (7881) is stable across the TCP → vsock transition.

---

## Wire Format

### Encoding

Newline-delimited JSON (NDJSON). Each message is a single JSON object
terminated by `\n`. Neither side sends a binary length prefix at the Phase 1
layer (contrast with `kenaz-workbench/internal/hostapi/`, which uses a 4-byte
big-endian length prefix — that protocol is host↔guest sigild; this one is
host↔in-VM harness).

**Rationale:** NDJSON is simpler to implement in stub form and easier to
probe with `socat` / `nc` during integration testing. Phase 8 may upgrade to
a binary-framed protocol if streaming task output requires it; the port and
the JSON schema shape will be preserved.

---

### Task Request (host → guest)

```json
{
  "command": "<string>",
  "args": { ... }
}
```

| Field     | Type             | Required | Description |
|-----------|------------------|----------|-------------|
| `command` | string           | yes      | Logical task name. Phase 1 stub accepts any value. Phase 8 routes on this to the harness graph kernel. |
| `args`    | JSON object/null | no       | Task-specific payload. Opaque to the transport layer; the harness adapter interprets it. |

**Phase 1 behaviour:** the stub accepts any `command` value and ignores `args`.

**Phase 8 examples:**
- `{"command":"noop"}` — liveness check (preserved from Phase 1).
- `{"command":"exec","args":{"argv":["ls","-la","/workspace"]}}` — shell exec.
- `{"command":"agent.task","args":{"task":"summarize /workspace/notes.md","model":"claude-3-5-sonnet"}}` — agent task dispatch.

---

### Task Response (guest → host)

```json
{
  "status": "ok" | "error",
  "stub": true,
  "error": "<string>"
}
```

| Field    | Type   | Required | Description |
|----------|--------|----------|-------------|
| `status` | string | yes      | `"ok"` on success; `"error"` on failure. |
| `stub`   | bool   | Phase 1 only | Present and `true` in Phase 1 stub responses. Phase 8 removes this field. Callers can use its presence to distinguish stub from real harness. |
| `error`  | string | no       | Human-readable error message. Only present when `status == "error"`. |

**Phase 8 additions** (not in Phase 1):
- `"result"`: task-specific result payload.
- `"stream_id"`: identifier for a follow-on streaming result channel (long-lived connection model).
- `"events"`: inline array of `harness.task.*` ledger events for callers that don't want to poll the host ledger.

---

## Smoke Probe (Phase 1)

The smoke script (`kenaz-workbench/scripts/smoke-macos.sh --image=headless`)
verifies the harness-vm service by:

1. Booting the headless Tart VM.
2. Getting the VM's NAT IP via `tart ip`.
3. Sending a no-op task over TCP:
   ```
   echo '{"command":"noop"}' | socat - TCP:<ip>:7881
   ```
   or via the orchestrator's `RunHarnessTask`:
   ```go
   result, err := orch.RunHarnessTask(ctx, orchestrator.HarnessTask{Command: "noop"})
   // expect: result.Stdout or result containing {"status":"ok","stub":true}
   ```
4. Asserting the response contains `"status":"ok"` and `"stub":true`.

---

## Security note (Phase 1 spike)

The Phase 1 TCP listener has **no authentication**. The guest-side
port 7881 is reachable from the host via Tart's NAT interface. Any process
on the host that can reach the VM's NAT IP can send arbitrary task RPCs.

This is intentional for the stub phase:
- The VM is ephemeral (created and destroyed per smoke run).
- No real task execution happens (stub always responds `ok`).

Phase 8 must add at minimum a shared-secret header (bootstrapped via the
vsock channel from the host orchestrator at VM spawn time) before the harness
executes real tool calls.

---

## Open Questions (for Phase 8)

1. **Stream multiplexing:** should streaming task output be a separate
   vsock channel (new connection per stream) or multiplexed over the
   control connection via a stream ID?

2. **Backpressure:** the harness graph kernel can produce large volumes of
   tool-call events. Should we rate-limit at the vsock layer or rely on
   TCP flow control?

3. **Task cancellation:** the host may want to cancel a running task (user
   presses stop). Define a `{"command":"cancel","args":{"task_id":"..."}}` RPC
   or a separate signal channel?

4. **Audit forwarding:** should the harness emit `task.start / task.complete`
   events directly to the in-VM sigild reporter (Phase 2 topology) or pass
   them back in the RPC response for the host to forward?

These are Phase 8 design questions. Raise them in the Phase 8 kick-off.
