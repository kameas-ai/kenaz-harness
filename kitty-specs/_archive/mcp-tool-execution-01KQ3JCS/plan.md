# Plan: MCP Tool-Call Execution Loop

**Mission:** mcp-tool-execution-01KQ3JCS
**Spec:** [spec.md](./spec.md)
**Status:** Draft
**Strategy:** five-step rollout, each step independently shippable + reversible

This plan turns the spec into discrete, mergeable units of work.
Each step has an owner-team scope, a test gate, and explicit
dependencies on the prerequisite missions.

## Prerequisites (must merge before Step 1)

| Mission | Status | What we need from it |
|---|---|---|
| C1 — MCP server lifecycle | Pending | `mcp.Pool.Invoke(ctx, server, tool, args) (result, error)` reliable; restart on crash; per-server status |
| C2 — Per-session overrides | Pending | `sessions.mcp_overrides` JSON column + accessor on Session |
| Mission B — Hook runner | In flight | `hooks.Runner.RunPreToolUse / RunPostToolUse` |
| Adapters emit `[]ToolUse` | Verify | Anthropic ✅ / Bedrock ✅ / OpenAI ⚠️ / OpenRouter ⚠️ |

If any prerequisite slips, Step 1 unblocks via shimming the missing
piece with a stub that returns the right shape, but stub paths
must be removed before Step 5 marks the mission complete.

## Step 1: `core/toolloop` skeleton + happy path (single-tool)

Lands the orchestrator package with the simplest possible loop:
one tool, no permissions, no hooks, no concurrency. End-to-end
works for US1 only.

**Files added:**
- `core/toolloop/loop.go` — `Loop` struct + `Run(ctx, sessionID, parentSubID, response, request) error`
- `core/toolloop/types.go` — `Resolution`, internal types
- `core/toolloop/loop_test.go` — fake registry + fake MCP pool, drive a one-tool round-trip end-to-end

**Files modified:**
- `core/rpc/views/llm/impl.go pump` — after stream-closed, if `Response.FinishReason == "tool_use"` and we have a `Loop` in scope, invoke `Loop.Run`. Otherwise current behavior.
- `core/rpc/api.go newLLMStack` — construct the `*toolloop.Loop` and pass via `llm.Config`.
- `core/llm/llm.go` — verify ToolUse accumulation; ensure adapters emit ToolUse deltas correctly.

**Test gate:**
- New unit tests in `core/toolloop` cover one-tool round-trip end-to-end.
- `go test -race -count=1 -short ./core/...` passes (no regressions).

**Out of scope for Step 1:** permissions, confirm-each, parallelism, hooks, audit, cancellation, iteration cap.

## Step 2: Permission gate + tool resolution

Adds the `PermissionResolver` and the merged-catalog logic
(global ∪ session, with per-tool allow / deny). Implements US3
("deny" tools) but defers the modal flow.

**Files added:**
- `core/toolloop/perms.go` — `PermissionResolver` + `ToolPolicy` types
- `core/toolloop/perms_test.go`

**Files modified:**
- `core/toolloop/loop.go` — calls `perms.Resolve(sessionID, server, tool)` between tool-detection and dispatch; emits synthetic `tool_blocked` results when `deny`.
- `core/session/types.go` — confirm `MCPOverrides` shape from C2; if not yet present, add a temp constant `[]Override{}` here so Step 2 can land before C2.

**Test gate:**
- US3 acceptance test: deny-listed tool → orchestrator emits a blocked result, model receives a tool-result message indicating the block, conversation continues.

## Step 3: Hooks integration + audit

Wires `pre_tool_use` / `post_tool_use` lifecycle events into
Mission B's hook runner. Lands the audit log emissions.

**Files modified:**
- `core/toolloop/loop.go` — calls `hooks.RunPreToolUse(event)` and `hooks.RunPostToolUse(event)`; honors `continue: false` from pre.
- `core/toolloop/audit.go` — new file, emits `tool_invoked` / `tool_failed` events via `audit.Emitter`.
- `core/hooks/runner.go` — adds `RunPreToolUse` / `RunPostToolUse` if Mission B did not already.

**Files added:**
- `core/toolloop/hooks_test.go` — fake hook runner verifies pre/post are called with the right event payloads, that a `continue: false` aborts the call.

**Test gate:**
- US7 acceptance test: a pre_tool_use hook that mutates args is observable in the dispatched MCP request (caught by the MCP capture fixture).
- Audit log shows redacted args for all calls.

## Step 4: Concurrency, cancellation, iteration cap, streaming feedback

The "make it real" step. Adds the parallelism cap (default 4),
cancellation path (Stop button cancels the whole loop), iteration
cap (default 8), and the new `StreamToolInvocation` chunk kind so
the chat surface can render tool chips inline.

**Files added:**
- `core/llm/llm.go` — adds `StreamToolInvocation` kind.
- `core/toolloop/concurrent.go` — bounded parallel dispatch using a semaphore.
- `frontend/src/components/chat/ToolInvocationChip.vue` — small inline chip rendering.

**Files modified:**
- `core/rpc/views/llm/impl.go pump` — handles new chunk kind, fans out to UI.
- `frontend/src/components/chat/MessageBubble.vue` — renders tool-invocation chips inline.
- `frontend/src/lib/useSession.ts` — new chunk kind threaded through the existing protocol.
- `core/toolloop/loop.go` — `maxIter` enforcement, ctx-driven cancel.

**Test gate:**
- US2 (sequential multi-tool, but our implementation is concurrent-with-ordered-merge) passes.
- US5 (iteration cap) hits the synthetic message at iter 8.
- FR-008 (cancellation within 1 s p95) measured in test.
- Frontend snapshot test for the chip rendering.

## Step 5: Confirm-each + UI modal + audit verification

The user-facing permission flow. Lands the `confirm_each` decision
loop with the modal + persistence, plus the cross-cutting audit
verification under load.

**Files added:**
- `core/toolloop/confirm.go` — `ConfirmationBus` impl; pending decisions are kept in-memory keyed by `pending_id`; decisions wake the loop via a channel.
- `core/rpc/bindings.go` — `Tools_PendingConfirmations` / `Tools_RespondToConfirmation` / `Tools_LoopStatus` / `Tools_CancelLoop` bindings.
- `frontend/src/components/chat/ToolConfirmModal.vue` — modal triggered by a new event topic `tools:confirmation-required`.
- `frontend/src/components/chat/__tests__/ToolConfirmModal.test.ts`.

**Files modified:**
- `core/toolloop/loop.go` — branches on `confirm_each` policy: emit pending → block on decision channel with a 5-min timeout.
- `frontend/src/views/sessions/SessionsView.vue` — listens for `tools:confirmation-required`, renders the modal, dispatches the response.

**Test gate:**
- US4 acceptance test: confirm modal renders, both decisions work, `allow_session` escalation observed.
- Confirmation timeout (5 min) cancels the loop with reason `confirm_timeout`.

## Cross-cutting concerns

### Telemetry

Each step adds structured logging via the existing `core/logging`
package. Required fields per record:
- `tool` (server.tool fully-qualified)
- `session_id`
- `parent_sub_id` (the LLM stream that originated this loop)
- `iter` (current iteration count)
- `latency_ms` (post-call only)
- `outcome` (success / blocked / failed / cancelled)

Tail `~/.kenaz/harness.log` to see every tool decision.

### Privacy

- `pre_tool_use` hooks see args BEFORE the redactor runs.
- The audit-log emission runs AFTER the redactor — args in audit records are SAFE.
- The `tool_confirmation_required` event payload to the frontend uses the redacted args (so the modal doesn't leak secrets to the rendered DOM if the args contain credentials).

### Failure modes table

| Failure | Detection | Handling |
|---|---|---|
| MCP server crashed | pool.Invoke returns ErrServerDown | One auto-retry, then `tool_failed` |
| Hook script timeout (>10s) | hooks.Runner timeout | Skip hook, log warn, continue |
| Adapter doesn't support tool calls | `Capabilities(model).Has(CapToolCalling) == false` | Spec validation rejects tool calls upstream; orchestrator never enters loop |
| LLM emits malformed tool args (not JSON) | json.Unmarshal in adapter | Synthetic tool-result `{"error": "invalid_args_json"}` to model |
| Tool result exceeds 256 KB | size check | Truncate + append `[truncated]` marker |

## Risk register

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Mission C1 slips → no MCP pool | Medium | Blocks Step 1 | Stub `mcp.Pool` interface; use a fake until C1 lands |
| Provider-specific tool-spec quirks | High | Steps 1-4 | Adapter-owned translation; integration test per kind |
| Concurrent tool calls cause MCP server thread-safety bugs | Medium | Step 4 | Per-server mutex around invocation; chaos test |
| Hook authoring footguns (e.g. infinite recursion) | Medium | Step 3 | Hook depth limit (default 1); detect re-entry into the loop |
| User confusion when iteration cap fires | Low | Step 4 | Synthetic message includes "increase max_iter in Settings" |

## Acceptance test plan (executed at end of Step 5)

Single integration test suite in
`core/toolloop/integration_test.go`:

1. **TestE2E_SingleTool_HappyPath** (US1)
2. **TestE2E_Sequential_TwoTools** (US2)
3. **TestE2E_DenyListed_Blocked** (US3)
4. **TestE2E_ConfirmEach_AllowOnce** (US4)
5. **TestE2E_ConfirmEach_AllowSession_Escalates** (US4)
6. **TestE2E_ToolFailure_ModelRecovers** (US5)
7. **TestE2E_AuditTrail_Redacted** (US6)
8. **TestE2E_PreToolUseHook_MutatesArgs** (US7)
9. **TestE2E_IterationCap_SyntheticMessage** (FR-006)
10. **TestE2E_StopButton_CancelsLoop** (FR-008)
11. **TestE2E_Concurrent_FourToolsSimultaneous** (FR-004)
12. **TestE2E_Timeout_ToolHangs** (NFR-004)

Each test uses an in-memory `MCPPool` fixture with deterministic
behaviors (configurable latency, error injection, args capture).
LLM provider is replaced by a scripted `llm.Stream` that emits a
fixed sequence of `tool_use` then `end_turn` events.

## Rollout / merge order

```
        ┌────────────────────┐
        │ Step 1: skeleton   │
        └──────────┬─────────┘
                   ▼
        ┌────────────────────┐
        │ Step 2: permissions│
        └──────────┬─────────┘
                   ▼
        ┌────────────────────┐
        │ Step 3: hooks+audit│
        └──────────┬─────────┘
                   ▼
        ┌────────────────────┐
        │ Step 4: concurrency│
        │   + streaming      │
        └──────────┬─────────┘
                   ▼
        ┌────────────────────┐
        │ Step 5: confirm-UI │
        │   + integration    │
        └────────────────────┘
```

Each step is its own branch, its own PR, mergeable into main on
its own. Steps 1-4 ship behind a feature flag
(`Settings.ToolLoop.Enabled`, default off) so the UI remains
chat-only until Step 5 unhides the modal flow.

## Open questions to resolve before Step 1

1. **Sub-agent dispatch:** Step 1 should be a clean subagent task once C1 lands. The agent prompt should be self-contained per the project's pattern; reference this plan + spec.
2. **Test fixtures:** decide on the in-memory MCPPool location — `core/mcp/fixture/` or `core/toolloop/internal/mcpfixture/`? Lean toward `core/mcp/fixture/` so other consumers (audit tests, capability tests) can reuse it.
3. **Versioning:** Anthropic, OpenAI, and Bedrock each version their tool-call wire format. Pin and document the versions tested; surface mismatch errors clearly.
4. **MCP server discovery:** does the harness do mDNS / dynamic discovery, or is everything statically configured? Statically configured for v1 (consistent with C1's spec).

## Definition of done

- All 12 integration tests pass.
- `~/.kenaz/harness.log` shows a clean trace for every supported user story.
- `Settings.ToolLoop.Enabled = true` makes the feature visible.
- Documentation in `docs/tool-loop.md` (created during Step 5) explains the user model, permission policies, and how to author custom hooks.
- The audit log filters at `/audit?tool=` show every call cleanly.
- A worked-example session (configure GitHub MCP server, ask about an issue, see the chip + final answer) reproduces in the Wails dev window in under 60 seconds.
