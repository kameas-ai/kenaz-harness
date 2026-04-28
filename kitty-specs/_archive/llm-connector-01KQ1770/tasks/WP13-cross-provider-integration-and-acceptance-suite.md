---
work_package_id: "WP13"
title: "Cross-provider integration suite and audit / cred-leakage scanner"
dependencies:
  - "WP06"
  - "WP07"
  - "WP08"
  - "WP09"
  - "WP10"
  - "WP11"
  - "WP12"
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
phase: "Phase 5 - Cross-cutting integration"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP13 – Cross-provider integration suite and audit / cred-leakage scanner

## Goal

Land the cross-cutting integration suite that proves all five day-one
providers satisfy streaming, tool calling, capability gating, retry,
audit, and cancellation through the same `Registry.Stream` surface;
ship a CI-gated cred-leakage scanner; collect the performance numbers
that defend the NFR budgets; and confirm the mission's success
criteria are met.

## Spec references

- SC-001 — Configure a new provider and run an agent in < 5 minutes.
- SC-002 — Switch active provider with no agent edit.
- SC-003 — 100 % of completed requests produce a complete event-log
  trail; verified by an audit harness that runs in CI.
- SC-004 — Zero plaintext credentials in the event log, bundle
  source, or process arguments; verified by an automated scanner in
  CI.
- SC-005 — Single transient failure invisible to caller ≥ 99 %.
- SC-007 — Connector overhead: cold first-call < 50 ms p95; warm
  steady-state < 20 ms p95.
- SC-008 — Bundle authors can opt into prompt caching, vision, tool
  calling, structured output, reasoning blocks against every day-one
  provider whose model + API supports each capability.
- NFR-001, NFR-002, NFR-003, NFR-004, NFR-005, NFR-006, NFR-007,
  NFR-008, NFR-009, NFR-010, NFR-011, NFR-012 — measured here.
- US1 / US2 / US3 / US4 acceptance scenarios — exercised end-to-end.

## Plan references

- §7 Phasing v1.0 — closing scope: ≥ 80 % `core/llm/**` line
  coverage, black-box per-provider integration tests via VCR
  fixtures by default, live-API tests opt-in.
- §4 Internal Layering — full pipeline exercised here.
- Charter §DIRECTIVE_028, §DIRECTIVE_030, §DIRECTIVE_036.

## Subtasks

- T001 — Add `core/llm/integration/` package with a fixture-driven
  matrix test: for each of the 5 day-one providers, run streaming
  hello, tool-call (where supported), vision (where supported),
  JSON mode (where supported), prompt caching (where supported),
  reasoning (where supported), cancellation. Each cell either
  passes or asserts `ErrCapabilityUnsupported` per the descriptor.
- T002 — Audit-completeness harness: for each successful run in
  T001, assert the event log contains `request_submitted`,
  `response_final`, usage, latency, and a fully ordered chain
  (SC-003). Run on a real on-disk event log under
  `t.TempDir()`.
- T003 — Cred-leakage scanner: a Go test that injects a synthetic
  API-key shape (e.g., `sk-ant-INJECTED-AAAA…`) into every field
  of a `GenerationRequest` (params, message text, attachment
  alt-text), runs the matrix, then `grep`s the persisted event
  log for the injected substring. Zero matches required (SC-004,
  NFR-007). Add this scanner to CI.
- T004 — Performance harness: benchmark cold first-call and
  warm-steady-state overhead per provider against a fake adapter
  that returns instantly; record p95 numbers. Fail on regression
  vs. NFR-001, NFR-002, SC-007.
- T005 — Retry-effectiveness harness: 100-trial run of "single
  transient then success" against each provider's fault-injecting
  fake; assert ≥ 99 % surfaced as success (NFR-006, SC-005).
- T006 — End-to-end provider-switch test: a single agent fixture
  bundle that declares Anthropic AND OpenRouter providers, runs
  the same agent against both, and verifies SC-002 with at most
  one bundle-config edit between runs.
- T007 — Live-API integration tests gated behind credentials
  (env vars `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`,
  `OPENROUTER_API_KEY`, AWS profile, `OLLAMA_HOST`). Skipped by
  default per DIRECTIVE_028; documented in the test file's
  package doc.

## Acceptance criteria

- `go test ./core/llm/...` passes (recorded fixtures); `go test
  -tags live ./core/llm/...` passes when credentials are present.
- `go test ./core/llm/integration/...` matrix shows every day-one
  provider passing every capability cell that the descriptor
  marks supported, and returning `ErrCapabilityUnsupported` for
  unsupported cells (SC-008).
- Cred-leakage scanner reports zero matches across the full
  matrix; the test is wired into CI as a required check (SC-004).
- Audit-completeness harness reports 100 % `request_submitted` →
  `response_final` ordering with usage + latency present (SC-003,
  NFR-012).
- Performance benchmark report committed to PR description with
  p95 numbers under the NFR-001 / NFR-002 / SC-007 budgets on the
  developer laptop.
- Retry-effectiveness harness reports ≥ 99 % success across all
  five providers (NFR-006, SC-005).
- Mission-level coverage report shows ≥ 80 % line coverage on
  `core/llm/**` (DIRECTIVE_030, charter Testing Standards).

## Files to create / modify

- `core/llm/integration/matrix_test.go`
- `core/llm/integration/audit_test.go`
- `core/llm/integration/credleak_test.go`
- `core/llm/integration/perf_bench_test.go`
- `core/llm/integration/retry_effectiveness_test.go`
- `core/llm/integration/provider_switch_test.go`
- `core/llm/integration/testdata/` (fixture bundles)
- `.github/workflows/llm-connector.yml` (CI: run matrix +
  cred-leakage + lint + coverage gate)

## Definition of done

- All subtasks complete; tests green; lint clean; coverage gate
  satisfied.
- CI workflow added and green on the feature branch.
- Mission-level success criteria SC-001 — SC-008 each have a
  corresponding green test or a documented sign-off.
- PR merged.
- Mission ready for `/spec-kitty.merge`.
