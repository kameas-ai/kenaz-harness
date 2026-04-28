---
work_package_id: "WP06"
title: "Tool catalog + five harness-native built-in tools"
dependencies:
  - "WP01"
  - "WP05"
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
  - "T007"
phase: "Phase 3 - Built-in tools"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP06 – Tool catalog + five harness-native built-in tools

## Goal

Implement the `Catalog` registry and the five day-one harness-native
tools (FR-018) under `core/mcp/server/tools/`. Each tool maps onto an
existing in-tree subsystem.

## Spec references

- FR-013 — Pluggable tool contract.
- FR-018 — Static curated tool set.
- C-008 — Tool implementations may import in-tree subsystems
  (`core/llm`, `core/event`, `core/secrets`, `core/bundle`,
  `core/session`, `core/scheduler`).

## Plan references

- §6.6 — `core/session` integration for `run_bundle`.
- §6.2 — `core/event` integration for `query_event_log`.
- Risk R8 — `query_event_log` exposes only redacted views.
- Open Question 7 — `run_bundle` allowlist.

## Subtasks

- T001 — Implement `core/mcp/server/tools.go`: `Catalog` struct with
  `Register(t Tool)`, `Get(name string) (Tool, bool)`, `List()
  []Tool`. Thread-safe.
- T002 — Implement `core/mcp/server/tools/runbundle.go`: tool
  `run_bundle`. Argument schema: `{bundle_id: string, args: object}`.
  Honors operator allowlist (`mcp.server.tools.run_bundle.allowed_bundles`).
  Calls `core/session.Executor.Start`; returns the started session id.
- T003 — Implement `core/mcp/server/tools/listbundles.go`: tool
  `list_bundles`. No arguments. Calls `core/bundle.Resolver` for the
  current resolved bundle set. Returns JSON array of `{id, version,
  source}` triples.
- T004 — Implement `core/mcp/server/tools/queryeventlog.go`: tool
  `query_event_log`. Arguments:
  `{session_id?: string, kind_prefix?: string, since?: string,
  limit?: integer (max 1000, default 100)}`. Calls
  `core/event.Log.Query`. Returns redacted events as a JSON array.
- T005 — Implement `core/mcp/server/tools/listsessions.go`: tool
  `list_sessions`. No arguments. Calls `core/session.Executor.List`;
  returns `[{id, bundle_id, started_at, status}]`.
- T006 — Implement `core/mcp/server/tools/listproviderprofiles.go`:
  tool `list_provider_profiles`. No arguments. Calls
  `core/llm.Registry.List`; returns profile summaries (NEVER
  resolved credentials).
- T007 — Tests: each tool unit-tested with fake subsystem
  dependencies; assert argument schema validation, result shape,
  redaction integrity for `query_event_log`, allowlist enforcement
  for `run_bundle`.

## Acceptance criteria

- `go test ./core/mcp/server/tools/...` passes; coverage ≥ 80 %.
- `query_event_log` test asserts that resolved credential fixtures in
  fake events do NOT appear in the tool's output (event-log redaction
  is upstream; this test confirms the tool does not bypass it).
- `run_bundle` allowlist test: bundle id not in allowlist returns
  `ErrPolicyDenied`.
- `list_provider_profiles` test asserts no credential bytes in result.
- Each tool's argument schema validates the documented shape.

## Files to create / modify

- `core/mcp/server/tools.go`
- `core/mcp/server/tools/runbundle.go`
- `core/mcp/server/tools/listbundles.go`
- `core/mcp/server/tools/queryeventlog.go`
- `core/mcp/server/tools/listsessions.go`
- `core/mcp/server/tools/listproviderprofiles.go`
- Tests alongside each tool file.

## Definition of done

- All subtasks complete; tests green; lint clean.
- PR merged into `feat/wire-integration`.
