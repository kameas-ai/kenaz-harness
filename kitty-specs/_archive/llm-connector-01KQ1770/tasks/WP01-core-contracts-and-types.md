---
work_package_id: "WP01"
title: "Core contracts and public types in core/llm"
dependencies: []
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
phase: "Phase 1 - Core skeleton"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP01 – Core contracts and public types in core/llm

## Goal

Establish the canonical, provider-agnostic Go types and interfaces in
`core/llm/llm.go` (extending the existing `adapter.go` stub) that every
downstream WP depends on: `Capability`, `CapabilityDescriptor`,
`CredentialReference`, `ProviderProfile`, `GenerationRequest`, `Stream`,
`Response`, `ProviderAdapter`, `Registry`, and the typed error taxonomy.

## Spec references

- FR-002 — Named provider profiles in bundle config (drives `ProviderProfile`).
- FR-003 — Indirect credential resolution only (drives `CredentialReference`).
- FR-004 — Unified streaming chat completion (drives `GenerationRequest` /
  `Stream`).
- FR-005 — Multi-turn conversation history (drives `Message` ordering).
- FR-006 — Tool calling (drives `ToolSpec`, `ToolUse`).
- FR-007 — Vision / image input (drives `Attachment`).
- FR-008 — Structured output / JSON mode (drives `JSONModeSpec`).
- FR-009 — Prompt caching opt-in (drives `CachingSpec`).
- FR-010 — Reasoning blocks (drives `ReasoningSpec`, `ReasoningBlock`).
- FR-011 — Token usage and cost reporting (drives `Usage`, `Cost`).
- FR-012 — Mid-stream cancellation (drives `Stream.Cancel()`).
- FR-013 — Unsupported-capability errors (drives `ErrCapabilityUnsupported`).
- FR-017 — Distinguish transient from non-transient errors (drives
  `ErrTransient`, `ErrAuth`, `ErrInvalidRequest`, etc.).
- FR-018 — Provider adapter extensibility (drives `ProviderAdapter` contract).
- C-001 — Architectural-integrity boundary (no provider SDK types leak here).
- C-005 — OSS / enterprise distribution split (contract is the single seam).

## Plan references

- §2 Architectural Placement — placement of `core/llm/llm.go` and the
  package boundary.
- §3 Public API (Illustrative Signatures) — full canonical signature set
  this WP materializes.
- §4 Internal Layering — pipeline stages whose interfaces depend on these
  types.

## Subtasks

- T001 — Audit existing `core/llm/adapter.go` stub and migrate / extend
  it into `core/llm/llm.go` without breaking existing callers.
- T002 — Define capability constants and `CapabilityDescriptor` struct.
- T003 — Define `CredentialReference`, `ProviderProfile`, validation
  hooks (kind, region requirement for bedrock, unique id).
- T004 — Define `GenerationRequest`, `Message`, `ToolSpec`, `Attachment`,
  `JSONModeSpec`, `CachingSpec`, `ReasoningSpec`, `RetryPolicy`.
- T005 — Define `Stream`, `StreamEvent` (with `Kind` enum: text, tool,
  reasoning, usage, finish), `Response`, `Usage`, `Cost`, `ContentPart`,
  `ToolUse`, `ReasoningBlock`.
- T006 — Define typed error taxonomy: `ErrCapabilityUnsupported`,
  `ErrCredentialResolution`, `ErrTransient`, `ErrRetryBudgetExhausted`,
  `ErrAuth`, `ErrInvalidRequest`, `ErrPolicyDenied`, `ErrCancelled`,
  with classification helpers (`IsTransient(err) bool`).

## Acceptance criteria

- `go build ./core/llm/...` succeeds with no provider SDK imports in
  `core/llm/llm.go`.
- `go vet ./core/llm/...` clean.
- Table-driven unit tests under `core/llm/llm_test.go` cover error
  classification (`IsTransient` over each typed error) and capability
  descriptor serialization round-trips. `go test ./core/llm/...` passes.
- The pre-existing stub callers (any usage of `adapter.go` in
  `core/`) compile against the extended types unchanged.
- No file under `core/llm/` (this WP only touches root) imports any
  provider SDK package; `go list -deps ./core/llm | grep -E
  '(anthropic|openai|aws|ollama)'` returns nothing.

## Files to create / modify

- `core/llm/llm.go` (canonical types; replaces / extends current
  `adapter.go`).
- `core/llm/errors.go` (typed error taxonomy + helpers).
- `core/llm/llm_test.go` (table-driven tests for error classification and
  type round-trips).
- `core/llm/doc.go` (package doc string referencing the architectural
  invariant).

## Definition of done

- All subtasks complete; tests green; `go vet` and `golangci-lint run`
  clean for `./core/llm/...`.
- Public types match plan §3 signatures; deviations recorded in commit
  message or ADR per DIRECTIVE_003.
- PR opened against `feat/llm-connector-01KQ1770` targeting `main`,
  ≥ 1 maintainer approval, squash-merge.
