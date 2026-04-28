---
work_package_id: "WP08"
title: "OpenRouter provider adapter"
dependencies:
  - "WP01"
  - "WP02"
  - "WP03"
  - "WP04"
  - "WP05"
  - "WP07"
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

# Work Package Prompt: WP08 – OpenRouter provider adapter

## Goal

Implement `core/llm/openrouter` — an OpenAI-compatible adapter pointed
at `openrouter.ai`, with model-specific capability discovery (default
plan resolution: query OpenRouter `/models` at startup, cache against
the resolved-graph snapshot for replay determinism), reasoning-model
support, and graceful capability gating for routed models that vary
in feature support.

## Spec references

- FR-001 — Day-one coverage (openrouter).
- FR-004 — Streaming.
- FR-006 — Tool calling (where the routed model exposes it).
- FR-010 — Reasoning blocks (OpenRouter routes reasoning-capable
  models).
- FR-011 — Usage / cost reporting.
- FR-013 — Typed unsupported-capability errors when the routed model
  doesn't support a requested feature.
- C-001 / C-005 — SDK isolation; OpenRouter is consumed via the same
  HTTP client shape as OpenAI but in its own package.
- US1 Acceptance Scenario 2 — switch active provider with no agent
  edit (Anthropic ↔ OpenRouter case).

## Plan references

- §2 Architectural Placement — `core/llm/openrouter/`.
- §6 Integration Points — capability discovery hook.
- §9 OQ-4 — capability-descriptor source for OpenRouter; default plan
  resolution = query `/models` at startup, cache in event-log-pinned
  snapshot for replay determinism.

## Subtasks

- T001 — Implement adapter using either an internal HTTP client OR
  the OpenAI SDK pointed at `https://openrouter.ai/api/v1` (decision
  in commit message; constraint is that no shared core code grows a
  dependency).
- T002 — Translate request body identical to OpenAI ChatCompletions
  with OpenRouter-specific headers (`HTTP-Referer`, `X-Title` from
  bundle metadata where available); preserve `Raw` passthrough.
- T003 — At `LoadProfiles` time, query OpenRouter `/models` once to
  populate per-model capability descriptors; cache the snapshot;
  emit a `capability_descriptor_snapshot` event into the event log
  for replay determinism. Network failure here is non-fatal: fall
  back to `capabilities/data/openrouter.yaml` (static baseline).
- T004 — Streaming SSE translation; reasoning-frame raw passthrough
  preserved per OQ-5 default.
- T005 — Error classification (same as OpenAI taxonomy);
  cancellation ≤ 1 s p99.
- T006 — Tests: VCR fixtures for streaming hello, tool-call against a
  tool-capable routed model, reasoning model run (assert `Reasoning`
  blocks present), `/models` snapshot caching test (second call uses
  cache), capability-gate denial when a routed model lacks a
  requested capability.

## Acceptance criteria

- `go test ./core/llm/openrouter/...` passes; coverage ≥ 80 %.
- Capability-discovery snapshot is recorded once per
  `Registry.LoadProfiles` and reused on subsequent profile lookups.
- Routed-model capability-gate denial emits
  `llm/capability_rejected` and never makes a wire call (US2.2).
- Reasoning-model fixture produces `Response.Reasoning` blocks with
  `Raw` passthrough non-empty.
- Cancellation test passes within 1 s p99.
- `/models`-fetch failure path: adapter degrades to static baseline
  and logs a non-fatal warning event.

## Files to create / modify

- `core/llm/openrouter/adapter.go`
- `core/llm/openrouter/models_snapshot.go`
- `core/llm/openrouter/translate_stream.go`
- `core/llm/openrouter/adapter_test.go`
- `core/llm/openrouter/testdata/*.json`
- `core/llm/capabilities/data/openrouter.yaml` (static baseline, may
  already exist from WP03)

## Definition of done

- All subtasks complete; tests green; lint clean.
- ADR snippet (or commit-message decision record per DIRECTIVE_003)
  captures the `/models` cache-on-load decision.
- Adapter self-registers via `init()`.
- PR merged.
