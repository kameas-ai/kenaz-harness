---
work_package_id: "WP11"
title: "Bundle artifact handler for kind: mcp_server"
dependencies:
  - "WP01"
  - "WP05"
  - "WP06"
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

# Work Package Prompt: WP11 – Bundle artifact handler for kind: mcp_server

## Goal

Register a bundle `ArtifactKindHandler` for `kind: mcp_server` so that
bundle resolution discovers, parses, validates, and activates MCP server
declarations into the Pool.

## Spec references

- FR-002 — Bundle artifact `kind: mcp_server`.
- C-004 — MCP server declarations live as bundle artifacts, not as a
  top-level configuration surface.

## Plan references

- §6.3 — bundle-format-resolver integration (Parse / Validate /
  Activate).
- §5.1 — artifact YAML format.

## Subtasks

- T001 — Implement
  `core/mcp/client/bundleartifact.go`:
  `Parse(bytes []byte) (ServerSpec, error)` — YAML/JSON unmarshal to
  `ServerSpec`; sets `schema_version` field.
- T002 — Implement
  `Validate(spec ServerSpec, ctx ManifestContext) error` — calls
  `spec_schema.Validate` (WP06) plus bundle-context checks: paths are
  inside the bundle's declared scope, ids are unique within the
  manifest set.
- T003 — Implement
  `Activate(spec ServerSpec, ctx ResolverContext) error` — registers
  the spec with the Pool's pending-spec list. The Pool's next
  `Reload(ctx, allActivatedSpecs)` materializes the updated set.
- T004 — Tests: a YAML fixture under `testdata/` exercises every
  validation rule (good case, bad transport, missing command for
  stdio, missing url for http, plaintext credential rejection,
  duplicate id).

## Acceptance criteria

- `go test ./core/mcp/client/...` (bundleartifact surface) passes;
  coverage ≥ 80 %.
- The handler conforms to whatever `ArtifactKindHandler` interface
  the bundle-format-resolver exposes; if the resolver mission has not
  yet shipped that interface, ship a thin adapter type that the
  resolver can plug into when ready (forward-compat).
- Test that the activation order (deterministic per
  `ResolvedGraph.activation_order`) is preserved.

## Files to create / modify

- `core/mcp/client/bundleartifact.go`
- `core/mcp/client/bundleartifact_test.go`
- `core/mcp/client/testdata/artifacts/*.yaml`

## Definition of done

- All subtasks complete; tests green; lint clean.
- PR merged into `feat/wire-integration`.
