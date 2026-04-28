---
work_package_id: "WP06"
title: "Pre-flight credential resolution and ServerSpec validation"
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
phase: "Phase 2 - Connection layer"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP06 – Pre-flight credential resolution and ServerSpec validation

## Goal

Validate every `ServerSpec` and pre-flight every credential reference at
`Pool.Open` time. Failures emit `mcp/preflight_failed` and never trigger
a transport spawn (FR-019). Defense-in-depth scan rejects inline
credential patterns in `Headers`/`Env` values.

## Spec references

- FR-002 — Bundle artifact validation rules.
- FR-003 — Indirect credential references only.
- FR-019 — Pre-flight credential resolution.
- C-002 — No inline plaintext credentials in declarations.

## Plan references

- §4 Internal Layering — `PreflightCoordinator`.
- §5 Data Model §1 — validation rules for `kind: mcp_server`.
- §6.1 — secrets-keychain integration (calls
  `core/secrets.Backend.PreflightAll`).

## Subtasks

- T001 — Implement `core/mcp/client/spec_schema.go`:
  `Validate(spec ServerSpec) error` — checks transport ∈ allowed,
  command/url presence depending on transport, headers/env values
  follow the credential-ref shape OR are non-secret strings (apply a
  defense-in-depth scanner from `core/secrets/lint`), roots are
  absolute paths, retry policy fields sane.
- T002 — Implement `core/mcp/client/preflight.go`:
  `Preflight(ctx, specs, secrets) []PreflightResult` — for each cred
  ref in headers / env, call `secrets.Resolve(ref)` and collect
  successes / failures. Never log resolved values.
- T003 — Wire `Preflight` into `Pool.Open` (WP05): preflight failures
  prevent the offending server from spawning but other servers proceed.
- T004 — Define `PreflightResult` struct:
  `{ServerID string, OK bool, Error error, Refs []ResolvedRef}` where
  `ResolvedRef` has only `Kind` + `Locator` (no secret value).
- T005 — Tests: a fake secrets backend that fails on a specific ref;
  preflight returns one failure + four successes; the failing server
  is marked unhealthy; `mcp/preflight_failed` event is emitted with
  the ref's `Kind`+`Locator` but no secret value.

## Acceptance criteria

- `go test ./core/mcp/client/...` passes; coverage ≥ 80 % over preflight
  + spec_schema.
- Negative test: a spec with `Headers: {Authorization: "sk-ant-xxx"}`
  (inline plaintext) is rejected at `Validate` with a clear error
  citing C-002.
- Negative test: a `bedrock`-style ref without a region is NOT this
  mission's concern (no Bedrock here); but a malformed cred ref is
  caught.
- Test that preflight emits NO event with the resolved secret value
  (assert the recorded event payload's bytes contain no occurrences
  of the secret string).

## Files to create / modify

- `core/mcp/client/spec_schema.go`
- `core/mcp/client/spec_schema_test.go`
- `core/mcp/client/preflight.go`
- `core/mcp/client/preflight_test.go`

## Definition of done

- All subtasks complete; tests green; lint clean.
- PR merged into `feat/wire-integration`.
