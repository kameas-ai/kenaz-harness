---
work_package_id: "WP06"
title: "Inbound A2A server endpoint and skill router"
dependencies:
  - "WP01"
  - "WP02"
  - "WP03"
  - "WP11"
  - "WP12"
  - "policy-engine:WP-allow-decision"
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
  - "T007"
phase: "Phase 6 - Inbound server"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP06 – Inbound A2A server endpoint and skill router

## Goal

Implement `core/acp/server/` — the inbound A2A endpoint coordinator —
plus `core/acp/internal/skillrouter/` mapping `(agent_id, skill_id)`
to a registered `SkillDispatcher`. Generate Agent Cards from
`expose_over_a2a` artifacts (WP03), serve them at the configured
well-known path on the chosen transport, accept incoming Tasks, run
the inbound policy gate, dispatch to the matching Skill, validate the
output schema, and emit the full set of inbound audit events.

## Spec references

- FR-003 — Inbound A2A server.
- FR-005 — Exposed-agent bundle artifact.
- FR-009 — Task lifecycle management (callee role).
- FR-014 — Policy gate for inbound calls.
- US2 Acceptance Scenarios 1, 2, 3 — exposure, success path,
  unknown-skill rejection.
- US7 Acceptance Scenarios 1, 2 — unallowlisted peer refusal,
  allowlisted peer success.
- Edge case: race at startup — listener bound only after bundle
  resolution completes.
- Edge case: `output_schema` violation → `task_failed` + protocol-
  level error.
- C-002 — Append-only event log immutability.

## Plan references

- §3 Public API — `A2AServer`, `SkillDispatcher`.
- §4 Internal Layering, "Inbound flow" — the exact sequencing this
  WP implements end-to-end.
- §6.5 policy-engine integration — `AllowInbound`.
- §8 R9 — bundle-resolver activation race; bind listener post-
  resolution only.

## Subtasks

- T001 — Implement `internal/skillrouter` with thread-safe
  `Register(agentID, skill, dispatcher)` and `Resolve(agentID,
  skillID) (Dispatcher, error)`. Unknown skill → `ErrSkillNotFound`.
- T002 — Implement `server.Server` struct: holds `SkillRouter`,
  `PolicyGuard`, `Verifier`, `envelope.Envelope`, `store.Store`,
  `events.Emitter`, plus a transport `Listener`. Implements
  `A2AServer` interface (Expose, Start, Stop).
- T003 — Implement Agent-Card generation: each `expose_over_a2a`
  spec from WP03 produces a `core/acp.AgentCard`. Cards are served
  on the configured well-known path
  (`/.well-known/agent.json`) by the transport's HTTP handler when
  applicable.
- T004 — Implement the inbound dispatch path per plan §4 "Inbound
  flow":
  1. Transport listener accepts → `envelope.AcceptTask` returns
     a typed inbound request.
  2. `PolicyGuard.AllowInbound(claimedPeerID, skill)`; on refusal
     emit `peer_auth_failed`, respond with typed protocol error,
     return without invoking Skill code (US7 Acceptance 1).
  3. `Verifier.Verify(claimedCard)` (FR-020 seam, no-op in v1).
  4. `SkillRouter.Resolve` → on unknown skill emit `task_failed`
     with `ErrSkillNotFound` and respond with the typed A2A error
     (US2 Acceptance 3).
  5. Mint Task (ULID), persist, emit `task_created` with
     `role=callee`.
  6. Invoke `SkillDispatcher.Dispatch`; validate the output against
     `Skill.OutputSchema`; on violation emit `task_failed` with
     `ErrSchemaViolation` and respond with a protocol-level error
     (the malformed payload still persists, redacted, for audit).
  7. Emit terminal `task_state_changed` and `envelope.Respond`.
- T005 — Bind the transport listener only after bundle resolution
  signals completion (R9 mitigation). Pre-bind connection attempts
  are refused at the transport layer (handled in WP07–WP10 transport
  packages).
- T006 — Tests using a standalone A2A client (WP02 envelope test-
  mode) against a fixture echo agent: assert US2 Acceptance 1
  (card served at well-known path), US2 Acceptance 2 (success +
  inbound task rows + events with `direction=inbound`), US2
  Acceptance 3 (unknown skill → typed protocol error +
  `task_failed`, Skill never invoked).
- T007 — Tests for US7: unallowlisted peer → refusal +
  `peer_auth_failed`; allowlisted peer → standard inbound audit
  trail; output-schema violation produces correct error path.

## Acceptance criteria

- `go test ./core/acp/server/...` and
  `go test ./core/acp/internal/skillrouter/...` pass; coverage ≥ 80%.
- US2 Acceptance Scenarios 1, 2, 3 reproduced as black-box tests.
- US7 Acceptance Scenarios 1, 2 reproduced as black-box tests.
- A pre-bind connection attempt during a slow bundle-resolution
  test is refused at the transport layer (R9 mitigation verified).
- Output-schema violations emit `task_failed` and persist the
  redacted malformed payload; log remains internally consistent
  (append-only, no rewrites).

## Files to create / modify

- `core/acp/server/server.go`
- `core/acp/server/cardgen.go` — generate `AgentCard` from
  `expose_over_a2a` spec.
- `core/acp/server/server_test.go`
- `core/acp/internal/skillrouter/router.go`
- `core/acp/internal/skillrouter/router_test.go`

## Definition of done

- All subtasks complete; tests green; lint clean.
- Cross-mission dependency on `policy-engine` `AllowInbound`
  documented; v1 default no-op `PolicyGuard` flagged with the
  CI lint per plan §6.5.
- No `core/acp/server/` or `core/acp/internal/skillrouter/`
  import of `a2aproject/a2a-go`.
- PR merged.
