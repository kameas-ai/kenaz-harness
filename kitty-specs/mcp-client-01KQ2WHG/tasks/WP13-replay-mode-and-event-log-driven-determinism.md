---
work_package_id: "WP13"
title: "Replay-mode transport for event-log determinism"
dependencies:
  - "WP01"
  - "WP04"
  - "WP07"
planning_base_branch: "feat/wire-integration"
merge_target_branch: "feat/wire-integration"
branch_strategy: "Planning artifacts were generated on feat/mcp-plans; completed changes merge into feat/wire-integration."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
phase: "Phase 4 - Bundle integration"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP13 – Replay-mode transport for event-log determinism

## Goal

Implement replay-mode: when the Pool is constructed with `replay: true`,
all wire calls are intercepted and answered from recorded MCP traffic in
the event log. No child processes spawn; no sockets open. This realizes
event-log spec FR-009 alignment for MCP traffic.

## Spec references

- FR-016 — Replay: a recorded session's MCP responses are returned
  without re-invoking the server.
- SC-004 — Replay determinism passes without spawning processes /
  opening sockets.

## Plan references

- §4 Internal Layering — `ReplayMode`.
- Open Question 7 — fail-fast on missing recording (default).

## Subtasks

- T001 — Implement `core/mcp/client/replay.go`: a `replayTransport`
  that implements `Transport` but reads recorded JSON-RPC frames from
  a `core/event.Log` query keyed by `session_id` + `server_id`.
- T002 — Implement a `Pool` constructor flag `replay: true` that
  swaps the transport factory for every server with `replayTransport`
  regardless of declared transport kind.
- T003 — Define behavior on missing recording: emit
  `mcp/replay_missing_recording` event and return
  `ErrReplayMissingRecording` (per Open Question 7 default).
- T004 — Tests: record an end-to-end stdio session into a fake event
  log; replay against the fake log; assert byte-identical responses;
  assert no `os/exec.Cmd` invocations occurred (verifiable via a test
  hook).

## Acceptance criteria

- `go test ./core/mcp/client/...` (replay surface) passes;
  coverage ≥ 80 %.
- SC-004 partial: replay test passes without spawning child processes
  or opening sockets (verified via test hook + mock event log).
- A missing-recording case returns the typed error and emits the
  warning event.

## Files to create / modify

- `core/mcp/client/replay.go`
- `core/mcp/client/replay_test.go`

## Definition of done

- All subtasks complete; tests green; lint clean.
- PR merged into `feat/wire-integration`.
