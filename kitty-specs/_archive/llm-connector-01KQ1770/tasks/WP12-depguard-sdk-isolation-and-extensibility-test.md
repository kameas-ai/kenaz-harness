---
work_package_id: "WP12"
title: "Depguard SDK-isolation rule and extensibility regression test"
dependencies:
  - "WP06"
  - "WP07"
  - "WP08"
  - "WP09"
  - "WP10"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
phase: "Phase 5 - Cross-cutting integration"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP12 – Depguard SDK-isolation rule and extensibility regression test

## Goal

Add a `golangci-lint` `depguard` rule that fails the build if any file
outside `core/llm/<provider>/` imports a provider SDK; add a
black-box "echo provider" extensibility regression test that proves a
new provider can be added end-to-end with zero edits to any other
`core/` package; document the contract in an ADR per DIRECTIVE_003.

## Spec references

- FR-018 — Provider adapter extensibility.
- C-001 — Architectural-integrity boundary.
- C-005 — OSS / enterprise distribution split.
- C-006 — SOC 2-readiness (review bar; ADR for material trade-offs).
- SC-006 — A new provider can be added end-to-end with no commits
  touching `core/` packages outside the new adapter's package
  (verified by a structural review check).
- US5 Acceptance Scenarios 1, 2.

## Plan references

- §2 Architectural Placement — invariants enumerated.
- §8 R1 — CI lint rule that fails any cross-package SDK import.
- Charter Check §DIRECTIVE_001, DIRECTIVE_003, DIRECTIVE_024.

## Subtasks

- T001 — Add / extend `.golangci.yml` with a `depguard` rule
  blocking `github.com/anthropics/...`,
  `github.com/sashabaranov/go-openai` (or chosen OpenAI SDK),
  `github.com/aws/aws-sdk-go-v2/service/bedrockruntime`, and any
  other provider SDK from being imported anywhere except
  `core/llm/<provider>/...`. Allow each SDK in exactly one
  per-provider package.
- T002 — Add `core/llm/registry/extensibility_test.go`: a build-tag
  gated test (`//go:build extensibility_test`) that compiles a
  throwaway `echoprovider` package living entirely outside `core/`
  (e.g., under `tools/extensibility-test/echoprovider/`),
  registers it through `Registry.RegisterAdapter`, declares a
  `kind: llm_provider` artifact for it, and asserts a streaming
  call succeeds.
- T003 — Add a structural CI check (script under `scripts/`)
  asserting a hypothetical commit that adds a new provider does
  not touch any file under `core/` outside its own adapter
  package. Ship the check as a script that `git diff --name-only`
  pipes through; document its use in the ADR.
- T004 — Author ADR `docs/adr/0001-llm-provider-adapter-contract.md`
  recording the adapter contract decision, SDK-isolation rule,
  registration mechanism, and the OSS/enterprise split (per
  DIRECTIVE_003).
- T005 — Tests: depguard rule fires on a synthetic violation (test
  fixture file that imports anthropic SDK from `core/event/`),
  extensibility test passes against the `echoprovider`.

## Acceptance criteria

- `golangci-lint run ./...` fails on a synthetic test that imports
  `github.com/anthropics/...` from `core/event/`.
- `golangci-lint run ./...` clean against the real repo (after WP06
  – WP10).
- `go test -tags extensibility_test ./core/llm/registry/...` passes;
  the `echoprovider` package contains zero imports of other
  `core/` packages aside from the public `core/llm` and
  `core/llm/registry` types.
- ADR file present at `docs/adr/0001-llm-provider-adapter-contract.md`
  and referenced from the connector README / package doc.
- Structural-check script exits non-zero on a synthetic diff that
  edits `core/event/` while adding a new adapter package.

## Files to create / modify

- `.golangci.yml` (depguard rule)
- `tools/extensibility-test/echoprovider/echo.go`
- `core/llm/registry/extensibility_test.go`
- `scripts/check-adapter-isolation.sh`
- `docs/adr/0001-llm-provider-adapter-contract.md`

## Definition of done

- All subtasks complete; tests green; lint clean.
- ADR linked from PR description.
- PR merged.
