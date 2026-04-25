---
work_package_id: "WP10"
title: "Sampling handler — route Session.Sample through core/llm.Registry"
dependencies:
  - "WP01"
  - "WP04"
planning_base_branch: "feat/wire-integration"
merge_target_branch: "feat/wire-integration"
branch_strategy: "Planning artifacts were generated on feat/mcp-plans; completed changes merge into feat/wire-integration."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
phase: "Phase 5 - Integration"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP10 – Sampling handler — route Session.Sample through core/llm.Registry

## Goal

Implement the sampling bridge: when a tool implementation calls
`Session.Sample(ctx, req)`, the handler routes the request through
`core/llm.Registry.Stream` under the bundle's resolved provider profile
and returns the result. Includes sampling-depth counter (default 4).

## Spec references

- FR-009 — Sampling: routed through harness LLM connector.
- C-007 — Sampling gated by policy engine (no-op v1 guard).

## Plan references

- §6.4 — llm-connector integration; default to internal-LLM sampling
  per FR-009.
- Risk R9 — sampling-depth runaway protection.
- Open Question 3 — profile selection default (`default_provider` or
  first registered).
- Open Question 5 — internal vs external LLM (default internal).

## Subtasks

- T001 — Implement `core/mcp/server/sampling.go`: `internalLLMSampler`
  struct holding a `core/llm.Registry` ref + `PolicyGuard`.
- T002 — Implement `Sample(ctx, sess, req SamplingRequest) (SamplingResponse, error)`:
  - Run `PolicyGuard.AllowSampling`; deny → `ErrPolicyDenied`.
  - Select provider profile (request hint OR bundle default OR first
    registered).
  - Construct `core/llm.GenerationRequest`.
  - Call `Registry.Stream`; collect into a non-streaming
    `SamplingResponse`.
  - Surface `ErrSamplingUnavailable` when no profile available;
    `ErrSamplingDepthExceeded` when depth > max.
- T003 — Wire `Sample` into the `session.Sample` method (WP04
  uses this).
- T004 — Tests: fake Registry returns recorded streaming responses;
  round-trip succeeds; profile-absent case; policy-denied case;
  depth-exceeded case (5 nested samples); audit emits
  `mcp.server/sampling_issued` and `mcp.server/sampling_completed`.

## Acceptance criteria

- `go test ./core/mcp/server/...` (sampling surface) passes;
  coverage ≥ 80 %.
- Tests verify both audit event kinds emit.
- The handler imports `core/llm` parent only; no provider sub-package
  leaks.

## Files to create / modify

- `core/mcp/server/sampling.go`
- `core/mcp/server/sampling_test.go`

## Definition of done

- All subtasks complete; tests green; lint clean.
- PR merged into `feat/wire-integration`.
