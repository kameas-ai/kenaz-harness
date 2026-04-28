---
work_package_id: "WP13"
title: "Diagnostics surface (harness bundle status / why / verify) and cross-cutting tests"
dependencies:
  - "WP01"
  - "WP02"
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
phase: "Phase 12 - Diagnostics"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP13 – Diagnostics surface and cross-cutting tests

## Goal

Ship the operator diagnostic commands (`harness bundle status`, `harness bundle why <artifact>`, `harness bundle verify`) and the cross-cutting test suite that proves the SC-001 through SC-006 measurable outcomes end-to-end. This is the integration WP that closes out the mission.

## Spec references

- FR-018 Diagnostic surface
- SC-001 publish-and-install in 10 minutes
- SC-002 100% byte-identical resolved graphs
- SC-003 100% hash verification
- SC-004 <500 ms p95 warm cache resolution (3 bundles, 15 artifacts)
- SC-005 new kind without core surgery
- SC-006 zero outbound network with warm cache
- NFR-001, NFR-002, NFR-003, NFR-004, NFR-005, NFR-006

## Plan references

- Plan §3.2 Resolver.Verify + VerifyTarget + VerifyReport
- Plan §6.4 event-log integration (`why` queries the event log + lockfile)
- Plan §7 v1 scope: diagnostic commands present
- Plan §8 R7 (CI benchmark gate on representative graph)

## Cross-mission dependencies

- The CLI surface (`harness bundle ...`) likely lives in `cmd/harness/` (or equivalent). This WP exposes the public functions; CLI wiring may land here or in a shared CLI mission depending on existing conventions.

## Subtasks

- T001 Implement `Resolver.Verify(ctx, target VerifyTarget) (*VerifyReport, error)`: re-check every content hash and signature in a graph or lockfile without fetching new artifacts; produce a per-artifact pass/fail report.
- T002 Implement `harness bundle status`: print the current `ResolvedGraph` summary — bundles, versions, channel sources, snapshot id, total artifact count, last lockfile update.
- T003 Implement `harness bundle why <artifact>`: trace why an artifact is in the active set — citing top-level config layer, source bundle, transitive dependency chain, content hash, channel of origin. Backed by a join of `ResolvedGraph` + event log.
- T004 Implement `harness bundle verify`: run `Resolver.Verify` over the active graph and print the report; non-zero exit on any failure.
- T005 Cross-cutting test suite:
  - Determinism harness: identical inputs across two runs and two OS targets produce identical `ResolvedGraph.ContentHash` (SC-002).
  - 100%-verification audit: instrument the fetch pipeline to assert that every activated artifact passed through hash verification (SC-003 / NFR-003).
  - Local-first audit: with a populated cache, run resolve under a network-blocking environment (e.g., `httptest` with denied default + no real channels) and confirm zero outbound calls (SC-006 / NFR-005).
  - Path-traversal containment: run the 0-escape audit suite across all channels (NFR-004).
  - Memory ceiling: peak RSS under 200 MB on a representative resolve (NFR-006).
  - Performance benchmark: 3 bundles / 15 artifacts warm-cache resolve under 500 ms p95; CI gate (NFR-001 / SC-004).
  - SC-005 demonstration: register a fresh kind handler in a test-only package, declare it in a fixture bundle, resolve and activate it, assert no edit to any `core/bundle/` package.
  - SC-001 walkthrough test: fixture bundle published to a local_path channel, referenced from a separate fixture top-level config, resolved successfully.

## Acceptance criteria

- All three CLI commands return correct, stable output for the canonical fixture bundle set.
- All eight cross-cutting tests pass and are wired into CI.
- `bundle why` traces transitive provenance for any artifact in the active set; a missing artifact yields a clear error.
- `bundle verify` exits non-zero on any hash or signature failure and zero otherwise.
- The performance benchmark gate is enforced with a documented baseline.

## Files to create/modify

- `core/bundle/resolver/verify.go` (new — Verify + VerifyReport)
- `core/bundle/diagnostics/status.go` (new — status command logic)
- `core/bundle/diagnostics/why.go` (new — why command logic)
- `cmd/harness/bundle_*.go` (new or extend — CLI wiring; align with existing CLI conventions)
- `core/bundle/internal/tests/...` (new — cross-cutting integration tests)
- `core/bundle/internal/bench/...` (new — Go benchmark for NFR-001 gate)

## Definition of done

- All acceptance criteria pass.
- CI runs the determinism, verification audit, local-first audit, path-traversal, memory, and performance tests.
- SC-005 demonstration test compiles and runs without any edit under `core/bundle/` outside the new test-only handler package.
- `harness bundle status / why / verify` CLI output is documented in a help string and stable enough for SOC 2 evidence collection.
- Plan §7 v1 scope is fully closed; v1.x and v2 items are not in scope.
