---
work_package_id: "WP14"
title: "Conformance and acceptance suite — stdio + streamable_http matrices"
dependencies:
  - "WP05"
  - "WP06"
  - "WP07"
  - "WP08"
  - "WP09"
  - "WP10"
  - "WP11"
  - "WP12"
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
phase: "Phase 6 - Acceptance"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP14 – Conformance and acceptance suite — stdio + streamable_http matrices

## Goal

Land the black-box acceptance suite that ties every WP together and
proves the spec's success criteria SC-001 through SC-006. Tests exercise
the harness as MCP server through both transports.

## Spec references

- All FRs and NFRs (acceptance closure).
- SC-001 — Stdio mode passes conformance run.
- SC-002 — Streamable-HTTP passes the same matrix.
- SC-003 — Audit suite: zero plaintext credentials.
- SC-004 — Replay determinism.
- SC-005 — Coverage ≥ 80 %.
- SC-006 — Refuses to start in stdio mode if stdout is unsuitable.

## Plan references

- §7 Phasing v1.0 — exit criteria.
- All risks (R1–R12) addressed via integration tests.

## Subtasks

- T001 — Build a fixture client harness in `testdata/conformance/`
  that:
  - For stdio: spawns the harness via `go run ./... mcp serve --stdio`
    in a subprocess, writes JSON-RPC frames to stdin, reads from
    stdout.
  - For streamable_http: opens an HTTP client against an
    `httptest.Server` instance bound to the harness's HTTP transport.
- T002 — Conformance matrix: for each transport, exercise:
  `initialize` → `tools/list` → each of the 5 built-in tools'
  `tools/call` happy path → `prompts/list` (against a fake bundle
  with one prompt) → `prompts/get` → `resources/list` → `resources/read`
  → server-initiated sampling → `notifications/cancelled` mid-call
  cancellation → `Shutdown`.
- T003 — Audit / redaction integration: feed each tool a fake
  credential pattern; assert recorded `mcp.server/*` event payloads
  do NOT contain the cleartext patterns.
- T004 — SC-006: stdout safety gate test — launch in stdio mode with
  stdout pointed at a fake colorizing writer; assert refusal to
  start.
- T005 — Replay test: record a session into a fake event log; replay
  the log and assert the recorded events reconstruct the call without
  re-executing tools.
- T006 — Coverage gate: `go test -coverprofile -race
  ./core/mcp/server/...`; assert coverage ≥ 80 % via a CI step.

## Acceptance criteria

- All matrix tests pass against both transports.
- Audit redaction test passes against the credential pattern fixtures.
- Stdout safety test passes (SC-006).
- Replay test passes; recorded session reconstructs without
  re-execution.
- Coverage ≥ 80 % across `core/mcp/server/**`.
- `go test -race ./core/mcp/server/...` passes.

## Files to create / modify

- `core/mcp/server/integration_test.go`
- `core/mcp/server/testdata/conformance/fake-bundle/*.yaml`
- `core/mcp/server/testdata/conformance/golden/*.json`
- `.github/workflows/coverage.yml` (or extend) — add
  `core/mcp/server/**` to coverage gates.

## Definition of done

- All subtasks complete; matrix tests green; coverage gates green;
  lint clean.
- PR merged into `feat/wire-integration`. Mission complete.
