---
work_package_id: "WP07"
title: "OpenAI provider adapter"
dependencies:
  - "WP01"
  - "WP02"
  - "WP03"
  - "WP04"
  - "WP05"
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
phase: "Phase 3 - Provider adapters"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP07 – OpenAI provider adapter

## Goal

Implement `core/llm/openai` — a `ProviderAdapter` for the OpenAI direct
API supporting streaming chat, multi-turn history, tool / function
calling, vision (gpt-4o family), JSON mode (response_format), usage
reporting, and mid-stream cancellation. OpenAI SDK code lives ONLY in
this package.

## Spec references

- FR-001 — Day-one coverage (openai).
- FR-004 / FR-005 — Streaming + multi-turn.
- FR-006 — Tool / function calling.
- FR-007 — Vision (image_url content parts).
- FR-008 — JSON mode (`response_format: json_object` /
  `json_schema`).
- FR-011 — Token usage.
- FR-012 — Mid-stream cancellation.
- FR-013 — Capability gate returns typed unsupported error for
  capabilities OpenAI does not expose for the chosen model
  (e.g., reasoning blocks on chat models).
- NFR-001 / NFR-002 / NFR-003 — Performance budgets.
- NFR-010 / NFR-011 — Streaming + tool-calling parity.
- C-001 / C-005 — SDK isolation.
- US2 Acceptance Scenarios 1–3.

## Plan references

- §2 Architectural Placement — `core/llm/openai/` SDK isolation.
- §3 Public API — adapter contract.
- §4 Internal Layering — error classification + cancellation.
- §9 OQ-1 — unified envelope with `Raw` passthrough.

## Subtasks

- T001 — Add OpenAI Go SDK to `go.mod`; assert single-package
  containment.
- T002 — `Adapter.Kind() = "openai"`; `Capabilities(model)` from
  `capabilities/data/openai.yaml`. Honor `Endpoint` field on
  `ProviderProfile` for OpenAI-compat hosts (this lays the
  groundwork for OpenRouter sharing infra in WP08).
- T003 — Translate `GenerationRequest` → ChatCompletions body:
  system + messages + tools (`functions` / `tools`) + image
  attachments (`image_url`) + `response_format` for JSON mode.
- T004 — Translate streaming SSE → `StreamEvent` (text deltas,
  tool_call deltas, finish_reason, usage update on stream end).
- T005 — Cancellation: close response body on `ctx.Done()` /
  `Stream.Cancel()` ≤ 1 s p99.
- T006 — Error classification: 408/425/429/5xx → `ErrTransient`;
  401/403 → `ErrAuth`; 400/404/422 → `ErrInvalidRequest`;
  content-policy refusal → non-retry typed error.
- T007 — Tests with VCR fixtures: streaming hello, tool-call,
  vision (image_url), JSON mode (schema-bound), cancellation; live
  API gated by `OPENAI_API_KEY`.

## Acceptance criteria

- `go test ./core/llm/openai/...` passes; coverage ≥ 80 %.
- Streaming hello-world VCR test produces ordered events through the
  audit emitter.
- Tool-call test returns a structured `ToolUse` matching recorded
  fixture.
- Vision test against gpt-4o fixture returns image-describing
  content.
- JSON-mode test with a schema returns parseable JSON content.
- Cancellation test: socket closed within 1 s p99.
- `core/llm/openai/` is the sole importer of the OpenAI SDK in the
  repo.

## Files to create / modify

- `core/llm/openai/adapter.go`
- `core/llm/openai/translate_request.go`
- `core/llm/openai/translate_stream.go`
- `core/llm/openai/errors.go`
- `core/llm/openai/adapter_test.go`
- `core/llm/openai/testdata/*.json`
- `go.mod`, `go.sum`

## Definition of done

- All subtasks complete; tests green; lint clean.
- Adapter self-registers via `init()`.
- PR merged.
