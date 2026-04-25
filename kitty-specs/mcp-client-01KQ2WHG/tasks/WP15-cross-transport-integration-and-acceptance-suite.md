---
work_package_id: "WP15"
title: "Cross-transport integration and acceptance suite"
dependencies:
  - "WP05"
  - "WP07"
  - "WP08"
  - "WP09"
  - "WP10"
  - "WP11"
  - "WP12"
  - "WP13"
planning_base_branch: "feat/wire-integration"
merge_target_branch: "feat/wire-integration"
branch_strategy: "Planning artifacts were generated on feat/mcp-plans; completed changes merge into feat/wire-integration."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
  - "T006"
phase: "Phase 5 - Acceptance"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP15 – Cross-transport integration and acceptance suite

## Goal

Land the black-box acceptance suite that ties every WP together and
proves the spec's success criteria SC-001 through SC-005. Tests run
against a fixture-server matrix that covers stdio, http_sse, and
streamable_http transports.

## Spec references

- All FRs and NFRs (acceptance closure).
- SC-001 — Day-one transports pass list / call / prompt / resource /
  sampling matrix.
- SC-002 — Pool lifecycle test against 5-server fixture.
- SC-003 — Audit suite: zero plaintext credentials.
- SC-004 — Replay determinism.
- SC-005 — Coverage ≥ 80 %.

## Plan references

- §7 Phasing v1.0 — exit criteria.
- All risks (R1–R11) addressed via integration tests.

## Subtasks

- T001 — Build a fixture-server matrix in `testdata/integration/`:
  - One stdio echo MCP server (Go binary) that supports tools, prompts,
    resources, sampling-callback patterns.
  - One http_sse fixture wired to `httptest.Server` exposing the same
    surface.
  - One streamable_http fixture wired to `httptest.Server`.
- T002 — Write the matrix integration test:
  for each transport ∈ {stdio, http_sse, streamable_http}:
    - Open pool with the transport's fixture server.
    - Run `tools/list`, `tools/call`, `prompts/list`, `prompts/get`,
      `resources/list`, `resources/read`, server-initiated sampling.
    - Assert byte-equal results against golden files.
- T003 — Pool lifecycle integration: open 5-server mixed pool, reload
  to a 5-server pool with 3 unchanged, close cleanly. Assert
  per-event-kind ordering invariants in the audit log.
- T004 — Audit / redaction integration: configure each fixture server
  with a fake credential pattern in its tool result (`sk-ant-fake`,
  `AKIA-fake`, AWS V4 signature shape). Assert recorded event
  payloads do NOT contain the cleartext patterns.
- T005 — Replay integration: record an integration session into a
  test event log; replay it; assert no `os/exec.Cmd` invocations and
  no `httptest.Server` requests during replay.
- T006 — Coverage gate: `go test -coverprofile -race ./core/mcp/...`;
  assert `core/mcp/client/**` coverage ≥ 80 % via a coverage CI step.

## Acceptance criteria

- All matrix tests pass against all three transports.
- Pool lifecycle test passes; audit kind ordering verified.
- Audit redaction test passes against the 3+ credential pattern
  fixtures.
- Replay test passes without spawning processes / opening sockets.
- Coverage ≥ 80 % across `core/mcp/client/**`.
- `go test -race ./core/mcp/...` passes.

## Files to create / modify

- `core/mcp/client/integration_test.go`
- `core/mcp/client/testdata/integration/echo-stdio/main.go`
- `core/mcp/client/testdata/integration/golden/*.json`
- `.github/workflows/coverage.yml` (or extend existing) — add
  `core/mcp/client/**` to coverage gates.

## Definition of done

- All subtasks complete; matrix tests green; coverage gates green;
  lint clean.
- PR merged into `feat/wire-integration`. Mission complete.
