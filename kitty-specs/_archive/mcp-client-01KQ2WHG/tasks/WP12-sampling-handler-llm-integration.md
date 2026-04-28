---
work_package_id: "WP12"
title: "Sampling handler — route server-initiated sampling to core/llm"
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
phase: "Phase 4 - Bundle integration"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP12 – Sampling handler — route server-initiated sampling to core/llm

## Goal

Implement the sampling handler bridge: when an MCP server issues a
`sampling/createMessage` request, route it through `core/llm.Registry`
under the bundle's resolved provider profile, and return the result to
the server. Includes a sampling-depth counter to prevent runaway loops.

## Spec references

- FR-009 — Sampling: server-initiated requests routed through harness
  LLM connector.
- C-007 — Sampling gated by policy engine; v1 ships with no-op guard.

## Plan references

- §6.4 — llm-connector integration.
- Risk R10 — sampling-depth cap.
- Open Question 1 — model-hint policy default.

## Subtasks

- T001 — Implement `core/mcp/client/sampling.go`:
  `LLMSamplingHandler` struct that wraps a `core/llm.Registry`, a
  policy guard, and a depth counter (default 4).
- T002 — Implement
  `Sample(ctx, server, req SamplingRequest) (SamplingResponse, error)`
  — convert the MCP request into a `core/llm.GenerationRequest`
  under the bundle's default provider profile; honor `model` hint
  only if policy allows; collect the LLM stream into a non-streaming
  MCP sampling response; surface `ErrSamplingUnavailable` when no
  profile, `ErrPolicyDenied` on guard refusal, and
  `ErrSamplingDepthExceeded` when depth > max.
- T003 — Wire `LLMSamplingHandler` into the Pool's connection setup
  (via `Pool.Open` deps) so every connection's `SamplingHandler` is
  the same handler.
- T004 — Tests: fake LLM Registry returns a recorded streaming
  response; sampling round-trip succeeds; profile-absent test asserts
  `ErrSamplingUnavailable`; depth-exceeded test asserts the typed
  error after 5 nested sampling calls; policy-denied test using a
  fake guard.

## Acceptance criteria

- `go test ./core/mcp/client/...` (sampling surface) passes;
  coverage ≥ 80 %.
- Round-trip test asserts both event log entries:
  `mcp/sampling_request` (server → handler) and
  `mcp/sampling_response` (handler → server).
- The handler imports `core/llm` from the parent package, NOT a
  provider sub-package; SDK isolation invariant intact.

## Files to create / modify

- `core/mcp/client/sampling.go`
- `core/mcp/client/sampling_test.go`

## Definition of done

- All subtasks complete; tests green; lint clean.
- PR merged into `feat/wire-integration`.
