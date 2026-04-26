# Spec: MCP Tool-Call Execution Loop

**Mission:** mcp-tool-execution-01KQ3JCS
**Mission type:** software-dev
**Owner:** kaneaz-harness
**Status:** Draft
**Created:** 2026-04-26
**Depends on:** mcp-server, mcp-client (server lifecycle), hooks runner (Mission B)

## 1. Problem statement

The harness can already (a) connect to MCP servers via the existing
`core/mcp` scaffolding and (b) emit per-tool stream events from
adapters that natively understand tool calls (Anthropic, Bedrock
Converse, OpenAI / OpenRouter via tool-use deltas). What it cannot
do yet is **close the loop** — when an LLM responds with a `tool_use`
finish reason, the harness today drops the request on the floor.
The user sees a stalled conversation; the conversation is
effectively single-turn even though the model wanted to call a tool.

This mission delivers the orchestrator that:

1. Detects a `tool_use` finish from the adapter.
2. Resolves each requested tool to a configured MCP server's
   capability and invokes it.
3. Threads the tool result back into the conversation as a `tool`
   role message.
4. Re-invokes the LLM with the augmented history.
5. Loops until the model returns a non-`tool_use` finish (`end_turn`,
   `max_tokens`, etc.) — bounded by an iteration limit.
6. Pipes each step through the hook runner (pre_tool_use /
   post_tool_use lifecycle events) so users can audit, deny, or
   transform tool calls in flight.

This is the surface that turns kaneaz-harness from "chat with LLM"
into "agent runtime."

## 2. Non-goals

- **MCP server lifecycle** (process start, restart, transport
  negotiation) is owned by Missions C1/C2 and is assumed working.
- **Tool exposure to the LLM** (translating MCP tool schemas into
  the provider's tool-spec format) is shared with C2 and not the
  primary deliverable here — we consume it.
- **Agent loops without tools** (function-calling against a single
  built-in tool, AutoGen-style multi-agent debate) — out of scope.
- **Cost/latency budgets enforced via tool-call governors** — design
  intentionally leaves a seam (pre_tool_use hook) but does not ship
  budget enforcement.

## 3. User stories

**US1 — Single-tool happy path.**
A user has the GitHub MCP server connected, asks "what's open in
issue #42?". The model emits a `tool_use` for `github.get_issue`.
The harness invokes the tool, gets JSON back, threads it as a tool
result, the model summarizes the response. Total elapsed time from
user-send to final assistant text: ≤ 3 s for a fast tool, ≤ 10 s
for a slower one.

**US2 — Sequential multi-tool.**
The model needs to call `filesystem.read_file` then
`github.create_issue`. The orchestrator handles them in order,
preserving each result in the conversation. The user sees a
streaming "thinking" indicator with tool-name annotations:
"calling github.create_issue…", and the answers compose into a
single final assistant turn.

**US3 — Per-call permission gate.**
A session has the `filesystem` server enabled, but `delete_file`
is on the deny list at the per-tool gate. The model attempts to
call `delete_file`. The orchestrator rejects the call with
`tool_blocked` and surfaces a typed `tool_result` to the model
saying "this tool is not authorized for this session." The model
adapts (e.g. asks the user, or chooses a different tool).

**US4 — Confirm-each.**
A `git.push` tool has `confirm_each` policy. The orchestrator
pauses, emits a `tool_confirmation_required` event to the frontend,
which renders a modal: "Server git wants to call push with args
{remote: origin, branch: main}. [Allow once] [Allow always for
this session] [Deny]." Until the user clicks, the conversation is
in a `awaiting_user` state. Click → orchestrator continues.

**US5 — Tool failure loop.**
Tool returns an error (e.g. 404 from the server). The error is
surfaced to the model as a tool result with `is_error=true`. The
model can retry, choose a different tool, or apologize and ask the
user. Iteration cap (default 8) bounds runaway loops.

**US6 — Audit trail.**
Every tool call lands in the audit log with: tool name, server,
session, user-visible summary of args (with redaction), result
status, latency, and the (sub_id, parent_message_id) correlation.
The `/audit` view filters by tool name.

**US7 — Hook-driven transformation.**
A user installs a `pre_tool_use` hook that redacts paths under
`~/.ssh/` from `filesystem.read_file` args. The orchestrator
applies the hook before dispatching, and the redacted version is
what reaches the MCP server. A `post_tool_use` hook persists every
tool result into the memory store (Mission B integration).

## 4. Functional requirements

**FR-001 — Tool-call detection.**
After the adapter's stream closes, if `Response.FinishReason ==
"tool_use"` and the accumulated `[]ToolUse` is non-empty, the
orchestrator enters tool-execution mode. Otherwise the loop exits
normally.

**FR-002 — Tool resolution.**
Each `ToolUse.Name` is resolved against the merged tool catalog:
global MCP servers ∪ per-session overrides, filtered by per-tool
allow/deny rules. Unresolved tools yield an immediate
`tool_blocked` synthetic result; the model is informed and can
recover.

**FR-003 — Hook lifecycle.**
For each tool call the orchestrator fires (in order):
- `pre_tool_use` — hook receives `{ session_id, tool_name, server,
  args, attempt_no }`, returns `{ continue: bool, args?: any,
  blocked_reason?: string }`. A `continue: false` short-circuits
  with `tool_blocked`.
- (orchestrator dispatches to MCP server)
- `post_tool_use` — hook receives `{ session_id, tool_name, server,
  args, result, error?, latency_ms }`. Side-effect only.

**FR-004 — Concurrent dispatch.**
Multiple `ToolUse` entries from a single LLM turn dispatch
concurrently (up to a configurable parallelism cap, default 4).
Results are joined in declared order before the next LLM
invocation. A failure of one tool does NOT cancel the others —
each result, success or error, lands in the conversation.

**FR-005 — Result threading.**
Each tool result becomes a `Message{Role: "tool", ToolCallID,
Content}` appended to the conversation. The provider-specific
adapter is responsible for translating to its native shape
(Anthropic `tool_result` blocks, OpenAI `tool` role messages,
Bedrock Converse `toolResult`).

**FR-006 — Iteration bound.**
The orchestrator runs at most N iterations per user turn (default
8, configurable per session). On exhaustion, it appends a synthetic
assistant message: "I attempted to call tools 8 times without
reaching a final answer; please clarify." The user turn closes.

**FR-007 — Permission gate.**
Per-tool policy is one of `auto_allow | confirm_each | deny`.
`confirm_each` halts the loop, emits a `tool_confirmation_required`
event with a unique `pending_id`, and waits for a
`Tools_RespondToConfirmation(pending_id, decision)` binding to
resolve. Decisions: `allow_once`, `allow_session` (escalates to
`auto_allow` for the session), `deny`.

**FR-008 — Cancellation.**
If the user clicks "Stop" mid-tool-loop, the orchestrator cancels
the in-flight MCP call (best-effort), drops any pending tool
calls, and emits a `tool_loop_cancelled` event. The conversation
returns to the idle state.

**FR-009 — Audit.**
Every tool call produces an audit-log event with kind
`tool_invoked` and structured payload (server, tool, args
redacted, result status, latency). Errors emit `tool_failed`
with the typed error class.

**FR-010 — Streaming feedback.**
While the orchestrator loops, the frontend receives
`tool_invocation_started{tool, server, args_summary}` and
`tool_invocation_finished{tool, status, summary}` events on the
existing `llm:stream-chunk` topic via a new chunk kind
`StreamToolInvocation`. The MessageBubble renders these as inline
chips: `🔧 github.get_issue → 200 OK (340ms)`.

## 5. Non-functional requirements

- **NFR-001:** Tool-loop overhead (orchestrator + hooks, NOT
  including the MCP server's own work) ≤ 50 ms per round at p99.
- **NFR-002:** All orchestrator state is per-`(sessionID, sub_id)`
  — concurrent tool loops in different sessions never share state.
- **NFR-003:** No credential or secret in any audit-log payload —
  args are redacted via the existing event-log redactor before
  emission.
- **NFR-004:** A misbehaving MCP server (hangs, returns malformed
  JSON) does not freeze other sessions; per-call timeout default
  30 s, configurable.
- **NFR-005:** The orchestrator runs entirely in-process, no
  additional daemon / sidecar.

## 6. Architectural sketch

```
┌────────────────────────────────────────────────────────────────┐
│                  rpc/views/llm/impl.go pump                    │
│  (existing) accumulates StreamText / StreamTool deltas         │
└────────────────────────────────────────────────────────────────┘
                           │ on stream-closed
                           ▼
                ┌──────────────────────┐
                │  ToolLoop.Run()      │  ← new core/toolloop package
                │  - inspect Response  │
                │  - if !tool_use:exit │
                └─────────┬────────────┘
                          │
            ┌─────────────┴─────────────┐
            │ for each ToolUse:         │
            │   pre_tool_use hook       │
            │   resolve(server, tool)   │
            │   permission gate         │
            │   dispatch via MCPPool    │
            │   post_tool_use hook      │
            │   thread result           │
            └─────────────┬─────────────┘
                          │
                          ▼
                 ┌────────────────┐
                 │  reg.Stream(   │  ← re-invoke the LLM
                 │   augmented    │     with appended tool
                 │   request)     │     results
                 └────────┬───────┘
                          │
                          ▼ (loop until !tool_use or iter cap)
                ┌──────────────────┐
                │   final assist   │
                │   committed via  │
                │   AppendMessage  │
                └──────────────────┘
```

## 7. Data shapes

```go
// New types in core/toolloop:
type Loop struct {
    reg          llm.Registry
    pool         mcp.Pool         // existing
    hooks        hooks.Runner     // from Mission B
    history      SessionHistoryRW // read + AppendMessage
    audit        audit.Emitter
    perms        PermissionResolver
    confirm      ConfirmationBus
    maxIter      int              // default 8
    parallel     int              // default 4
    callTimeout  time.Duration    // default 30s
}

type Resolution struct {
    Server   string
    Tool     string
    Args     []byte           // raw JSON
    Policy   ToolPolicy       // auto_allow / confirm_each / deny
}

type ToolPolicy string // "auto_allow" | "confirm_each" | "deny"

type Pending struct {
    ID         string  // ULID
    SessionID  string
    Tool       string
    Server     string
    ArgsRedacted string
    DecidedAt  time.Time
    Decision   string  // allow_once / allow_session / deny
}
```

## 8. Wails surface additions

```
Tools_PendingConfirmations(sessionID) []Pending
Tools_RespondToConfirmation(pendingID, decision)
Tools_LoopStatus(sessionID) (state, iter_count, last_tool)
Tools_CancelLoop(sessionID)
```

Plus a new stream-chunk kind on the existing `llm:stream-chunk`
topic so the chat surface can render inline tool chips.

## 9. Edge cases

| # | Case                                 | Handling                                               |
|---|--------------------------------------|--------------------------------------------------------|
| 1 | LLM emits an unknown tool name       | Synthetic `tool_blocked` result, model continues       |
| 2 | MCP server crashed between calls     | Per-call retry once; on failure → `tool_failed` result |
| 3 | Tool returns >256 KB                 | Truncate to 256 KB + append "[truncated]" marker       |
| 4 | Tool times out (default 30 s)        | Cancel the MCP call, emit `tool_failed`                |
| 5 | Hook denies the call                 | `tool_blocked` with the hook-provided reason           |
| 6 | Confirmation timeout (5 min default) | `tool_loop_cancelled` with reason `confirm_timeout`    |
| 7 | User clicks Stop mid-loop            | Cancel in-flight, drop pending, close stream          |
| 8 | Adapter does not advertise tool calls| Skip orchestrator entirely (no-op)                     |
| 9 | Iteration cap reached                | Synthetic assistant message, finish reason `iter_cap` |
|10 | Concurrent tools share an args path  | Each call gets a private args buffer (dup before disp) |

## 10. Acceptance criteria

A1. Single-tool happy path (US1) returns a final answer with the
    tool result correctly threaded, end-to-end latency observed
    ≤ 10 s for the GitHub `get_issue` call.

A2. Sequential multi-tool (US2) executes both tools, results
    appear in the audit log in declared order, the final assistant
    turn references both tool outputs.

A3. Permission deny (US3) results in a `tool_blocked` chunk
    visible in the chat (rendered as a red 🔧 chip), the model
    receives a structured tool-result message indicating the block,
    and the conversation continues without interruption.

A4. Confirm-each (US4) halts the loop, the modal renders, both
    decisions (allow_once vs allow_session) work and the
    allow_session escalation is observable in subsequent calls
    not pausing.

A5. Iteration cap (US5 worst-case) bounds runaway loops; the
    synthetic assistant message renders with finish reason
    `iter_cap`.

A6. Audit log (US6) shows redacted args, latency, server, tool,
    and a stable correlation id linking the loop to the parent
    user turn.

A7. Hook-driven (US7) transformation is end-to-end visible: a
    `pre_tool_use` hook that mutates args is reflected in the MCP
    server's received request (caught by an MCP capture fixture
    in tests).

A8. Cancellation (FR-008) happens within ≤ 1 s p95 of the user
    clicking Stop while a tool is in flight.

## 11. Open questions

- **OQ1:** Should the orchestrator support tool-result
  *streaming* from MCP servers, or only request/response?
  Several MCP servers emit progress notifications. Initial design
  assumes request/response; revisit in v2 if the UX value
  warrants.
- **OQ2:** What's the right default `maxIter`? Claude Code uses
  effectively unlimited with cost ceilings. We start at 8 to
  prevent runaway loops; promote to a setting in Settings →
  Advanced.
- **OQ3:** Should `confirm_each` decisions persist across
  Wails restarts, or are they session-only? Spec defaults to
  session-only; long-term-allow is a manual policy edit in /tools.
- **OQ4:** How do tool results interact with cross-session memory
  (Mission B)? The post_tool_use hook fires; if memory.persist is
  installed and configured to ingest tool results, they get
  embedded. Recommended default: tool results are NOT auto-stored
  unless the user explicitly opts in (privacy: tool outputs may
  carry sensitive data). Confirm before implementation.
- **OQ5:** Per-provider tool-spec translation lives in adapters
  today. Is there enough variance across kinds (anthropic vs
  openai vs bedrock-converse) that we need a translation matrix
  in `core/toolloop`, or can each adapter own it? Initial design:
  adapters own it, expose via a new `ToolSpecAdapter` interface.

## 12. Dependencies

- **C1 — MCP server lifecycle** must land first. The orchestrator
  expects `pool.Invoke(ctx, server, tool, args) (result, error)`
  to return reliably.
- **C2 — Per-session tool overrides** provides the data the
  permission resolver reads.
- **Mission B — Hook runner** must expose `RunPreToolUse` and
  `RunPostToolUse` lifecycle events.
- **Anthropic / Bedrock / OpenAI / OpenRouter adapters** must each
  emit `[]ToolUse` deltas correctly. Anthropic + Bedrock are
  largely there; OpenAI / OpenRouter need verification.

## 13. Out-of-scope follow-ups (parking lot)

- Tool *cost* governors (per-call $ ceiling, daily cap).
- Tool result *caching* (memoize identical args within a session).
- Cross-session tool macros ("call X then Y").
- Visual programming surface (no-code tool chains).
