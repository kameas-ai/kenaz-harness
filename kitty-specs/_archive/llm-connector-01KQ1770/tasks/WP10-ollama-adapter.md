---
work_package_id: "WP10"
title: "Local Ollama provider adapter"
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
phase: "Phase 3 - Provider adapters"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP10 – Local Ollama provider adapter

## Goal

Implement `core/llm/ollama` — a `ProviderAdapter` that talks to a local
Ollama daemon (default `127.0.0.1:11434`, configurable via
`ProviderProfile.Endpoint`) supporting streaming chat, multi-turn,
tool calling on tool-capable local models, vision on multimodal local
models, JSON mode, usage reporting, and cancellation. No third-party
SDK — direct HTTP/JSONL over `net/http`.

## Spec references

- FR-001 — Day-one coverage (ollama).
- FR-004 / FR-005 — Streaming + multi-turn.
- FR-006 — Tool calling on tool-capable local models.
- FR-007 — Vision on local multimodal models.
- FR-008 — JSON mode (`format: json`).
- FR-011 — Usage reporting (token counts from Ollama response).
- FR-012 — Cancellation.
- NFR-009 — Local-first guarantee: Ollama is the only adapter that
  can satisfy the harness's "no network egress" mode for LLM calls
  (the rest are remote).
- C-001 / C-005 — Adapter isolation.

## Plan references

- §2 Architectural Placement — `core/llm/ollama/` (HTTP client →
  127.0.0.1:11434).
- §3 Public API — adapter contract.
- §4 Internal Layering — error classification + cancellation.

## Subtasks

- T001 — Implement `Adapter.Kind() = "ollama"`;
  `Capabilities(model)` from `capabilities/data/ollama.yaml`. Honor
  `ProviderProfile.Endpoint` (default `http://127.0.0.1:11434`).
  Credentials are typically empty for local Ollama; the adapter
  accepts a no-op `CredentialReference` (kind=`none` or empty
  locator) without failing preflight.
- T002 — Translate `GenerationRequest` → Ollama `/api/chat`
  request body (messages, tools, images base64, format).
- T003 — Stream JSONL line-by-line; map to `StreamEvent`
  (text deltas, tool_use deltas, done event with usage).
- T004 — Cancellation: cancel HTTP request via `ctx`; close body;
  ≤ 1 s p99.
- T005 — Error classification: connection-refused / timeout /
  503 → `ErrTransient`; 4xx → `ErrInvalidRequest`; missing-model
  (404 from Ollama) → typed `ErrInvalidRequest` with helpful
  "ollama pull <model>" hint.
- T006 — Tests: VCR fixtures via `httptest` server simulating
  Ollama JSONL responses for streaming hello, tool-call,
  vision (multimodal model), JSON mode, cancellation,
  connection-refused (transient), missing-model (non-transient).

## Acceptance criteria

- `go test ./core/llm/ollama/...` passes; coverage ≥ 80 %.
- VCR streaming hello-world test runs entirely against an
  `httptest.Server`; no network egress.
- Connection-refused returns `ErrTransient` and the retry middleware
  retries per default policy.
- Missing-model error is non-retry and contains the `ollama pull`
  hint.
- Cancellation test: socket close ≤ 1 s p99.
- No external SDK imports introduced; only `net/http`,
  `encoding/json`, stdlib.

## Files to create / modify

- `core/llm/ollama/adapter.go`
- `core/llm/ollama/translate_request.go`
- `core/llm/ollama/translate_stream.go`
- `core/llm/ollama/errors.go`
- `core/llm/ollama/adapter_test.go`
- `core/llm/ollama/testdata/*.jsonl`

## Definition of done

- All subtasks complete; tests green; lint clean.
- Adapter self-registers via `init()`.
- PR merged.
