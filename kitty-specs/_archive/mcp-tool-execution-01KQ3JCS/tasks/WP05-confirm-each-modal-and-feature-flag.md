---
work_package_id: "WP05"
title: "Confirm-each modal flow + feature flag + integration suite"
dependencies:
  - "WP01"
  - "WP02"
  - "WP03"
  - "WP04"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
  - "T006"
phase: "Phase 5 — Confirm-each + integration verification"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-26T00:30:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP05 — Confirm-each modal flow + feature flag + integration suite

## Goal

Land the user-facing permission modal (`confirm_each` policy),
flip the feature flag from off to user-controllable, and run the
12-test integration acceptance suite. This is the WP that
declares the mission shippable.

## Spec references

- Spec: US4, FR-007, all acceptance criteria A1–A8.
- Plan: § "Step 5", § "Acceptance test plan".

## Prerequisites

WP01–WP04 all merged.

## Subtasks

- **T001 — ConfirmationBus.** New
  `core/toolloop/confirm.go` with an in-memory
  `ConfirmationBus` keyed by `pending_id` (ULID). Decisions
  arrive via a channel. Pending decisions surface via the
  Wails surface (T002). 5-minute timeout → loop cancels with
  `confirm_timeout` reason.
- **T002 — Wails bindings.** Add `Tools_PendingConfirmations
  (sessionID) []Pending`, `Tools_RespondToConfirmation
  (pendingID, decision)`, `Tools_LoopStatus(sessionID)
  (state, iter_count, last_tool)`, `Tools_CancelLoop(sessionID)`
  in `core/rpc/bindings.go` + matching view-scoped interface
  in `core/rpc/views/tools` (new package).
- **T003 — Loop integration.** Branch on `policy ==
  "confirm_each"`: emit `tools:confirmation-required` event
  with redacted args; block on the bus until a decision
  arrives (or 5-min timeout). On `allow_session`, escalate the
  policy entry for the session lifetime (in-memory map keyed
  by session id).
- **T004 — Frontend modal.**
  `frontend/src/components/chat/ToolConfirmModal.vue` listens
  for `tools:confirmation-required`, renders the modal with
  redacted args + 3 buttons (Allow once / Allow always for
  this session / Deny). Wire into `SessionsView.vue`. Tests
  in `frontend/src/components/chat/__tests__/ToolConfirmModal.test.ts`.
- **T005 — Feature flag.** Add `Settings.ToolLoop.Enabled`
  (default false). The pump only invokes the tool loop when
  this is true. Settings UI gains a toggle in
  `SettingsView.vue` under "Advanced". Survives Wails
  restarts via the existing settings store.
- **T006 — Integration test suite.** Create
  `core/toolloop/integration_test.go` with all 12 named tests
  from the plan's "Acceptance test plan" section. Run them
  all green. The suite uses a deterministic scripted
  `llm.Stream` fixture (emits a configurable sequence of
  tool_use → end_turn) and the in-memory MCP pool with
  configurable behaviors.

## Acceptance

All 8 spec acceptance criteria pass:
- A1 (US1 single-tool happy path)
- A2 (US2 sequential multi-tool)
- A3 (US3 deny-listed)
- A4 (US4 confirm-each + escalation)
- A5 (US5 iteration cap)
- A6 (US6 audit redacted)
- A7 (US7 hook arg mutation)
- A8 (US8 cancellation ≤ 1 s)

The 12 integration tests pass. `Settings.ToolLoop.Enabled =
true` makes the feature visible in `wails dev`. The audit
filter at `/audit?tool=...` works. The mission's worked
example (configure GitHub MCP server, ask about an issue,
see the chip + final answer) reproduces in under 60 seconds.

## Documentation

Create `docs/tool-loop.md` with three sections:
- **User model** — what tool calling looks like in chat.
- **Permissions** — auto_allow / confirm_each / deny + the
  modal flow.
- **Authoring custom hooks** — link to the hooks mission docs;
  show a worked pre_tool_use example that redacts sensitive
  paths.

## Branch strategy

Branch `wp05-confirm-and-integration` off `main`. Merge back
when all acceptance criteria pass and the Settings toggle is
wired. This commit closes the mission.
