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

## Ledger emission (criterion #5 / M1 finding #11)

In addition to the per-connection RPC stream above (which is consumed by the
host orchestrator), the in-VM harness pushes **task lifecycle records to the
reporter ingest endpoint** so the host audit ledger (`reporter_events`) shows
agent activity attributed to the correct workbench.

This is a *side channel*, independent of the RPC connection: the RPC stream is
for the live UI; the ledger records are for the durable audit timeline.

- **Endpoint:** the Unix socket named by `SIGIL_INGEST_SOCKET` (the in-VM
  sigil reporter's terminal-ingest socket). When the env var is unset, ledger
  emission is disabled and the task surface runs unchanged (dev / no-reporter
  path).
- **Encoding:** NDJSON, one record per connection. Each `emit` dials, writes
  one line, and closes — fire-and-forget with bounded dial/write deadlines so a
  slow or absent reporter never stalls a task.
- **Record shape** (matches `sigil internal/event.Event` so the reporter's
  `TerminalSource` ingests it without translation):

  ```json
  {
    "kind": "ai",
    "source": "harness",
    "payload": {
      "phase": "task.start",            // task.start | tool_call | task.complete | task.cancelled
      "task_id": "<id>",
      "workbench_id": "<id>",           // from SIGIL_WORKBENCH_ID
      "tool": "<name>",                 // tool_call only
      "prompt_len": 42                  // task.start only — length, NOT the prompt text
    },
    "timestamp": "2026-06-07T12:00:00Z"
  }
  ```

- **Lifecycle emitted per task:** one `task.start`, zero-or-more `tool_call`,
  and exactly one terminal record (`task.complete` or `task.cancelled`).
- **Privacy:** records carry only structural metadata. Prompt text, tool
  arguments, and task output are NEVER written to the ledger. `task.start`
  carries `prompt_len` (a count), never `prompt`.

**Deferred (downstream hops, not in this repo):** the in-VM
`sigild-vm-reporter` must register a terminal-ingest source listening on
`SIGIL_INGEST_SOCKET` (sigil finding #1), and the host `kenaz` ingest must
accept and persist the reporter's spooled events. Until those land, the harness
emits to the socket but no `reporter_events` row appears — that end-to-end close
is a human-witnessed operator smoke.

---

## Phase G — read-only view surfaces

**Status:** IN-PROGRESS (read paths). **Owner:** Phase G (`kenaz-harness` server adapter + `kenaz` host client).

Phase G adds **read-only** query RPCs that let the kenaz host render its
IDE-merger views (Sessions / Tools / Memory / Workflows / Providers) from the
data the in-VM harness already owns. They are additive to the Phase 8 task
surface: same NDJSON framing, same auth handshake, same long-lived connection.
A read RPC is request/response and runs independently of the task busy-guard —
a read can be served while a task streams `task.running` chunks on the same
connection.

Server implementation: `cmd/harness-vm/readservice.go` wraps the in-process
`rpc.API` view accessors (the same surface the harness's own Wails app binds)
via `rpc.New(core.New(Options{DataDir}))`. Only read accessors are called;
write paths are deferred (and would fail headless — harness mutations emit Wails
runtime events that need a lifecycle context the in-VM service lacks).

### Common shape

```jsonc
C→S: {"kind":"<surface>.<verb>","req_id":"<id>", ...args}
S→C: {"kind":"<surface>.<verb>.ok","req_id":"<id>", ...payload}
S→C: {"kind":"<surface>.<verb>.error","req_id":"<id>","code":"...","message_truncated":"..."}
```

`req_id` is echoed so the host correlates the response on the shared
connection. Error `code` values: `bad_request` (missing/invalid arg),
`server_error` (the in-process API returned an error), `unavailable` (the read
surface failed to bootstrap — e.g. locked/absent data dir; the task surface
still works).

### Caps

Result sets are bounded server-side (no cursor pagination in v1 — the host
shows the most-recent N): `maxListItems=200`, `maxMessageItems=500`,
per-message content `maxMessageBytes=4096`, memory chunk summary
`maxChunkSummaryByte=256`, per-unit body preview `maxUnitBodyPreviewBy=4096`.
Content fields truncated at a UTF-8 rune boundary carry `"truncated":true`.

### Surfaces

```jsonc
// Sessions
C→S: {"kind":"sessions.list","req_id":"<id>"}
S→C: {"kind":"sessions.list.ok","req_id":"<id>","sessions":[{"id","name","createdAt","updatedAt","kind?","projectId?"}]}
C→S: {"kind":"sessions.get","req_id":"<id>","sessionID":"<id>"}
S→C: {"kind":"sessions.get.ok","req_id":"<id>","session":{...}}
C→S: {"kind":"sessions.list_messages","req_id":"<id>","sessionID":"<id>"}
S→C: {"kind":"sessions.list_messages.ok","req_id":"<id>","messages":[{"id","role","content","truncated?","createdAt"}]}

// Tools (MCP recipe registry)
C→S: {"kind":"tools.list","req_id":"<id>"}
S→C: {"kind":"tools.list.ok","req_id":"<id>","tools":[{"name","kind":"mcp","enabled"}]}

// Memory
C→S: {"kind":"memory.list_chunks","req_id":"<id>"}
S→C: {"kind":"memory.list_chunks.ok","req_id":"<id>","chunks":[{"id","scopeKind","scopeId?","summary","createdAt","pinned?"}]}

// Providers (LLM)
C→S: {"kind":"providers.list","req_id":"<id>"}
S→C: {"kind":"providers.list.ok","req_id":"<id>","providers":[{"id","name","kind?","present","sourceTag?"}]}

// Workflows
C→S: {"kind":"workflows.list","req_id":"<id>"}
S→C: {"kind":"workflows.list.ok","req_id":"<id>","workflows":[{"id","name","description?","stepCount","source"}]}

// Units (unified context+artifacts store — Spec 056 kenaz.harness.run-control)
C→S: {"kind":"units.list","req_id":"<id>"}
S→C: {"kind":"units.list.ok","req_id":"<id>","units":[{"id","kind","scope","scopeId?","classification","version","title","bodyPreview","truncated?","createdAt","updatedAt"}]}

// Artifacts (units.list narrowed to kind=="artifact" — the run "review artifacts" path)
C→S: {"kind":"artifacts.list","req_id":"<id>"}
S→C: {"kind":"artifacts.list.ok","req_id":"<id>","artifacts":[{...same wireUnit shape as units.list}]}
```

### Privacy (load-bearing — enforced by `readservice_test.go`)

- **Memory:** the `summary` is the chunk *title* bounded to 256 UTF-8 bytes.
  The response NEVER carries `Chunk.Content` (raw chunk text), `FilesRead`, or
  `FilesModified`. Full-chunk fetch (`memory.get_chunk`) is a deferred RPC that
  will require a per-fetch audit ledger entry.
- **Providers:** the response carries only `present` (is a credential
  configured?) and `sourceTag` (the credential-reference *kind* — e.g.
  `keychain` / `aws-profile`). It NEVER carries the credential value, a redacted
  preview, the locator string, or the byte length of any of those.
- **Sessions / Workflows:** message bodies are truncated at 4 KiB; the workflow
  list carries metadata only (no YAML body — that needs `secrets/lint`
  redaction, deferred to `workflows.get`).
- **Units / Artifacts:** metadata + a `bodyPreview` truncated at 4 KiB (UTF-8
  boundary). The raw `Metadata` JSON blob (may carry CAS hashes / extension
  data) is NEVER forwarded; no unit body crosses the wire uncapped.

### Run-control — `task.start` `run_params` widening (Spec 056, `kenaz.harness.run-control`)

`task.start` widens **additively** from `{task_id, prompt}` to
`{task_id, prompt, run_params?}`. `run_params` and every sub-key are OPTIONAL;
an **absent** `run_params` reproduces today's exact dispatch behaviour. The
terminal-event wire (`task.running` / `task.complete` / `task.cancelled` /
`task.error`) is UNCHANGED.

```jsonc
C→S: {"kind":"task.start","task_id":"<id>","prompt":"<prompt>",
      "run_params":{                       // optional; snake_case sub-keys, all optional
        "workflow_preset":"<id|name>",      // core/workflows/builtin preset selector
        "system_prompt_ref":"<ref>",        // reserved — no in-VM consumer yet (Phase 1)
        "autonomy_tier":"strict|cautious|default|bold|autonomous",
        "review_rigor":"<opaque>",          // reserved — no in-VM consumer yet (Phase 1)
        "policy_strictness":"<opaque>"      // reserved — no in-VM consumer yet (Phase 1)
      }}
```

Phase-1 server mapping (`cmd/harness-vm`):
- **`workflow_preset`** (live consumer): resolved against the embedded builtin
  catalog (id or display name, case-insensitive). The resolved step sequence
  drives the graph's node sequence, so the selection is observable on the run's
  ledger trail (one `tool_call` per preset step, in order — Spec 056 AC-5). An
  unresolvable preset ⇒ `task.error{code:"unknown_preset"}` (never marks busy).
- **`autonomy_tier`**: validated against `core/autonomy.ParseTier`; an unknown
  tier ⇒ `task.error{code:"bad_request"}`. Plumbed-not-enforced in the headless
  in-VM graph path (no live `session.Spec` / autonomy resolver there yet).
- **`system_prompt_ref` / `review_rigor` / `policy_strictness`**: parsed and
  threaded, but have no consumer in the in-VM graph runner yet (their
  Cedar/workflow knobs live in the full session engine). Reserved for a later
  phase; see the Spec 056 CONCERNS.

Privacy: `run_params` carries only structural selectors (preset names, tier
labels, refs) — never prompt text, message bodies, or diffs. The ledger
`task.start` still carries `prompt_len` only.

### Deferred (not in this surface)

Write paths everywhere; `memory.get_chunk` (full text + audit); `workflows.get`
(YAML body with secret redaction); cursor pagination; the `agent_feed.*` push
stream (per-workbench task lifecycle → host AgentFeed panel).

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
