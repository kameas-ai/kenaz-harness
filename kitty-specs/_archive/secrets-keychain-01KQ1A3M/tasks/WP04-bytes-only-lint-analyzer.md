---
work_package_id: "WP04"
title: "go/analysis lint analyzer enforcing []byte-only Secret invariant"
dependencies:
  - "WP03"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001: Implement go/analysis Analyzer flagging string-typed credential fields"
  - "T002: Flag credential-bearing functions returning string"
  - "T003: Flag string(secretBytes) conversions outside test files"
  - "T004: Implement //nolint:secret-bytes escape hatch with required justification"
  - "T005: Wire analyzer into golangci-lint as a custom plugin"
  - "T006: Positive (string-typed) and negative ([]byte-typed) fixture tests"
  - "T007: CI gate: blocking on PR for the analyzer"
phase: "Phase 4 - Lint Enforcement"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP04 – go/analysis lint analyzer enforcing []byte-only Secret invariant

## Goal

Ship the custom `go/analysis` analyzer that mechanically enforces the `[]byte`-only Secret invariant across `core/secrets/...`. The analyzer runs in CI via golangci-lint's custom-plugin facility and is the second of two layers (alongside code review) protecting against `string`-typed credential drift. Without it, D7 is a polite suggestion; with it, the rule is a build break.

## Spec references

- FR-013 (Zeroize after use): only achievable when secrets are `[]byte`; lint enforces the precondition.
- NFR-003 (Plaintext leakage): mechanical enforcement against `string`-typed credential fields prevents an entire class of leak.
- C-001 (Architectural integrity).
- Key Entities: Secret ("Backed by `[]byte`. Never `string`.").

## Plan references

- §11 Lint plan (Secret-as-`[]byte` enforcement): the canonical specification of this WP.
- §2 Architectural placement → `core/secrets/lint/` subpackage.
- §7 Phasing → v1.0 ships the analyzer + golangci-lint wiring; CI-blocking.
- §8 Risk register → R6 (lint enforcement may miss interface/generic cases) — this WP must address it.
- §12 Acceptance mapping → FR-013, NFR-003 partially map here.

## Subtasks

- Implement a `go/analysis` Analyzer at `core/secrets/lint/lint.go`.
- Flag any struct field declared in a package under `core/secrets/...` whose name matches `(?i)(secret|credential|key|token|password|api_?key)` AND whose type is `string`.
- Flag any function in `core/secrets/...` returning `string` whose name suggests credential-bearing semantics.
- Flag any conversion `string(secretBytes)` in non-test files where `secretBytes` is a `[]byte` originating from a `Secret.Use` closure (best-effort static reachability per plan §8 R6 — document limits).
- Implement the `//nolint:secret-bytes` escape hatch: the analyzer permits a directive only when accompanied by a non-empty justification comment, and only inside `_test.go` files.
- Wire the analyzer into the project's `golangci-lint` configuration via the custom plugin facility (`.golangci.yml` plus a small `cmd/secret-bytes-lint/main.go` if the plugin contract requires it).
- Provide analyzer fixture tests: positive cases (string-typed `APIKey`, `Token`, etc.) and negative cases (`[]byte`-typed equivalents).
- Add a CI step (or document the existing one) that fails when the analyzer reports any finding outside `_test.go` files.

## Acceptance criteria

- The analyzer flags `string`-typed credential fields under `core/secrets/...` in fixtures and in any production code added in WP01–WP03.
- The analyzer is wired into `golangci-lint run` and is CI-blocking on PR.
- The escape hatch only works in `_test.go` files and only with a non-empty justification.
- Documented limits per plan R6: interface-typed and generic-typed cases are noted as residual coverage gaps mitigated by code review.
- Tests in `core/secrets/lint/` cover every rule with positive/negative fixtures.
- Charter quality gates pass.

## Files to create / modify

- Create `core/secrets/lint/lint.go`.
- Create `core/secrets/lint/lint_test.go` and `core/secrets/lint/testdata/` fixtures.
- Possibly create `cmd/secret-bytes-lint/main.go` (golangci-lint custom plugin entry point).
- Modify `.golangci.yml` (or add it if not present) to register the analyzer.

## Definition of done

- WP03's `Secret` impls pass the analyzer cleanly (sanity check).
- Adding a deliberately string-typed credential field in any `core/secrets` file fails CI (red-then-green demonstration).
- Architectural integrity preserved (analyzer package imports no backend SDKs).
- Handoff: subsequent backend WPs (WP08–WP13) inherit this gate automatically.
