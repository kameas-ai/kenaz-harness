---
work_package_id: "WP07"
title: "mcp_server_allowlist and mcp_capability_allowlist control kinds"
dependencies:
  - "WP02"
  - "WP03"
  - "WP04"
  - "WP06"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
phase: "Phase 7 - mcp_server_allowlist + mcp_capability_allowlist"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP07 – mcp_server_allowlist and mcp_capability_allowlist control kinds

## Goal

Land `mcp_server_allowlist` (which MCP servers may be installed /
activated) and `mcp_capability_allowlist` (which capabilities of an
allowed server may be invoked) as registered control kinds. Both use
the `core/policy/clauses/` layout established in WP06.

## Cross-mission dependency

- **core/mcp** (consumer subsystem): server activation calls
  `Evaluate` with `Action.Kind() == "mcp.server.activate"`; capability
  dispatch calls with `"mcp.capability.invoke"`. If the MCP package's
  hook points are not yet present, this WP defines the action-kind
  names + adapter and surfaces the cross-mission gap in the PR body.

## Spec references

- FR-004 (control catalog v1 — MCP server allowlist + capability
  allowlist).
- FR-005 (extensibility — kinds in their own packages).
- FR-008 (denial taxonomy — `ReasonNotInAllowlist`,
  `ReasonCapabilityNotPermitted`).
- NFR-006 (control-catalog parity — each kind enforced by a consumer).
- User Story 1 (org allowlist of MCP servers).

## Plan references

- Plan §2 — `clauses/mcp_server_allowlist/`,
  `clauses/mcp_capability_allowlist/`.
- Plan §6 — consumer row for `core/mcp`.
- Plan §4 strict-narrowing — set-intersection semantics from WP04.

## Subtasks

- T001: Create `core/policy/clauses/mcp_server_allowlist/` with
  `params: { allow: [server_id...] }`. Lowering matches
  `Action.Kind() == "mcp.server.activate"` against `inputs.server_id`.
  `NarrowingMerge: semantics.SetIntersect`. Default fail-closed.
  Denial reason: `ReasonNotInAllowlist`.
- T002: Create `core/policy/clauses/mcp_capability_allowlist/` with
  `params: { allow: { server_id: [capability_name...] } }`. Lowering
  matches `Action.Kind() == "mcp.capability.invoke"` against
  `inputs.server_id` + `inputs.capability_name`. `NarrowingMerge`:
  per-server set-intersection (reuse `MapOfSetIntersect` from WP06).
  Denial reason: `ReasonCapabilityNotPermitted`.
- T003: Per-kind tests covering schema, lowering golden files,
  narrowing matrix, and end-to-end Evaluate for each Action kind.
  Include a scenario where `mcp_capability_allowlist` references a
  server not in the merged `mcp_server_allowlist` — validator emits
  an `unreachable_clause` warning (per WP04 finding taxonomy).
- T004: Register kinds via `init()`. Add consumer adapters
  (`adapter.go`) for `core/mcp` to use. Update
  `core/policy/registry_test.go` catalog assertion.

## Acceptance criteria

- Both kinds register at process start.
- A representative org policy with both clauses denies an MCP server
  not in the allowlist with `ReasonNotInAllowlist`, and denies a
  capability call against an allowed server but disallowed capability
  with `ReasonCapabilityNotPermitted`.
- Capability narrowing: parent allows {file.read, file.write} on
  server X, child allows {file.read} → effective {file.read} on
  server X. Child attempt to add {file.exec} → rejected.
- Unreachable-clause warning is emitted when capability allowlist
  references a server not in the merged server allowlist.
- Lowering output is byte-stable.

## Files to create/modify

- Create `core/policy/clauses/mcp_server_allowlist/{kind.go, schema.go,
  lower.go, merge.go, adapter.go}` plus `*_test.go`.
- Create `core/policy/clauses/mcp_capability_allowlist/{kind.go,
  schema.go, lower.go, merge.go, adapter.go}` plus `*_test.go`.
- Create golden fixtures under each package's `testdata/golden/`.
- Modify `core/policy/registry_test.go` for catalog presence.
- Modify `core/policy/layer/merge.go` only if the
  unreachable-clause cross-kind check needs a hook (kept generic).

## Definition of done

- Acceptance criteria pass.
- Charter quality gates clean.
- Cross-mission dependency on `core/mcp` documented in PR body.
- Conventional-commit message; commit attributed per DIRECTIVE_029.
