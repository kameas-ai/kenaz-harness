---
work_package_id: "WP06"
title: "Anthropic provider adapter"
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

# Work Package Prompt: WP06 – Anthropic provider adapter

## Goal

Implement `core/llm/anthropic` — a `ProviderAdapter` for Anthropic
direct API supporting streaming chat, multi-turn history, tool calling,
vision, JSON output, prompt caching (cache_control breakpoints),
extended thinking / reasoning blocks, usage reporting, and prompt
cancellation. Anthropic SDK code lives ONLY in this package.

## Spec references

- FR-001 — Day-one provider coverage (anthropic).
- FR-004 / FR-005 — Streaming + multi-turn.
- FR-006 — Tool calling.
- FR-007 — Vision (image inputs).
- FR-008 — JSON mode.
- FR-009 — Prompt caching (Anthropic `cache_control` is the canonical
  exemplar).
- FR-010 — Reasoning / thinking blocks.
- FR-011 — Token usage (input, output, cached read/write).
- FR-012 — Mid-stream cancellation.
- NFR-001 — Connector overhead < 20 ms p95 (non-streaming).
- NFR-002 — First-token streaming overhead < 10 ms p95.
- NFR-003 — Cancellation responsiveness < 1 s p99.
- NFR-010 — Day-one parity for streaming text.
- NFR-011 — Day-one parity for tool calling (anthropic in scope).
- C-001 / C-005 — SDK isolation.
- US2 Acceptance Scenarios 1–4.

## Plan references

- §2 Architectural Placement — `core/llm/anthropic/` is the only
  place that imports the Anthropic SDK.
- §4 Internal Layering — adapter returns typed `ErrTransient` for
  retryable conditions; honors `context.Context` and `Stream.Cancel()`.
- §3 Public API — `ProviderAdapter` contract, `Stream`, `Response`.
- §9 OQ-1 — unified normalized envelope with `Raw json.RawMessage`
  passthrough for advanced cases (default).

## Subtasks

- T001 — Add Anthropic Go SDK dependency in `go.mod`; confirm only
  `core/llm/anthropic/` imports it.
- T002 — Implement `Adapter.Kind() = "anthropic"` and
  `Adapter.Capabilities(model)` returning the descriptor sourced from
  `core/llm/capabilities/data/anthropic.yaml` (WP03).
- T003 — Implement request translation: `GenerationRequest` →
  Anthropic `messages` body (system, messages, tools, attachments
  for vision, `cache_control` markers from `CachingSpec`,
  `thinking` block from `ReasoningSpec`, response_format for JSON
  mode where supported).
- T004 — Implement streaming response translation: SSE events →
  `StreamEvent` (text deltas, tool_use deltas, thinking deltas,
  message_delta usage updates, message_stop finish event); reasoning
  raw frames preserved under `Raw` per plan OQ-5 default.
- T005 — Implement `Stream.Cancel()` that closes the underlying HTTP
  response body within 1 s p99 (R5 mitigation; honor `ctx` deadline).
- T006 — Map upstream errors to typed taxonomy: 408/425/429/5xx →
  `ErrTransient`; 401/403 → `ErrAuth`; 4xx (non-retry) →
  `ErrInvalidRequest`; content-policy → non-retry typed error.
- T007 — Tests: VCR-style recorded fixtures under
  `core/llm/anthropic/testdata/` for: streaming hello-world,
  tool-call dispatch, vision happy path, prompt-cache hit (cache
  read tokens > 0), reasoning block emission, mid-stream cancel
  (assert socket closed). Live-API test gated by
  `ANTHROPIC_API_KEY` (off by default per DIRECTIVE_028).

## Acceptance criteria

- `go test ./core/llm/anthropic/...` passes (recorded fixtures);
  coverage ≥ 80 %.
- VCR fixture for streaming hello-world produces ordered
  `request_submitted` → N × `stream_chunk` → `response_final` events
  via the audit emitter.
- Tool-call test: response includes a `ToolUse` with name + input
  matching the recorded fixture.
- Vision test: image-bearing message returns a non-empty content
  describing the image.
- Prompt-cache test: `Response.Usage.CachedInputRead > 0` on second
  identical-prefix request (US2 Acceptance 4 against Anthropic).
- Cancellation test: `Stream.Cancel()` returns within 1 s p99 against
  a slow-stream fake; subsequent `Stream.Final()` returns
  `ErrCancelled`.
- No file outside `core/llm/anthropic/` imports the Anthropic SDK
  (assert via `go list -deps ./... | grep anthropic` pinned to one
  package).

## Files to create / modify

- `core/llm/anthropic/adapter.go`
- `core/llm/anthropic/translate_request.go`
- `core/llm/anthropic/translate_stream.go`
- `core/llm/anthropic/errors.go`
- `core/llm/anthropic/adapter_test.go`
- `core/llm/anthropic/testdata/*.json` (VCR fixtures)
- `go.mod`, `go.sum` (Anthropic SDK)

## Definition of done

- All subtasks complete; tests green; lint clean.
- Adapter self-registers via `init()` against the `core/llm/registry`
  default registry (WP02).
- PR merged.
