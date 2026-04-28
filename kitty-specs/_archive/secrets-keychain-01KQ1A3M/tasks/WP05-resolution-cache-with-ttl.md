---
work_package_id: "WP05"
title: "Resolution cache with TTL and rotation invalidation"
dependencies:
  - "WP02"
  - "WP03"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001: Implement TTL cache keyed by hash(kind, locator)"
  - "T002: Default TTL 60s; per-backend override"
  - "T003: Eviction calls Secret.Destroy()"
  - "T004: Implement Resolver.Invalidate(ref) API"
  - "T005: Implement backend-driven invalidation hook"
  - "T006: Microbenchmark for warm-cache p99 < 1 ms (NFR-001)"
  - "T007: Unit tests for TTL expiry, invalidation, eviction zeroize"
phase: "Phase 5 - Cache"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP05 – Resolution cache with TTL and rotation invalidation

## Goal

Land the in-process TTL cache that sits between consumer calls and backend resolution. The cache stores `Secret` handles (not raw bytes), evicts on TTL expiry, supports operator-driven and consumer-driven invalidation, and calls `Secret.Destroy()` on every eviction so zeroization is automatic. This is the warm-path NFR-001 driver.

## Spec references

- FR-010 (Resolution cache with TTL): resolved credentials cached briefly to avoid backend round-trips on hot paths; TTL is configurable per backend.
- FR-011 (Cache invalidation on rotation): cache invalidated within a configurable TTL so rotation takes effect promptly.
- NFR-001 (Resolution latency, warm cache): p99 < 1 ms.
- NFR-007 (Rotation propagation): rotated credential takes effect within 5× TTL p99 across all consumers.
- Edge case: "operator rotates the underlying credential while the harness holds it cached: the cache is invalidated within a configurable TTL".

## Plan references

- §2 Architectural placement → `core/secrets/cache/` subpackage.
- §4 Internal layering → step 2 (cache check) and "Cache rotation invalidation" subsection.
- §7 Phasing → v1.0 default TTL 60s; rotation invalidation API.
- §9 Open questions Q1 → adopts 60s default; per-backend override available.
- §12 Acceptance mapping → FR-010, FR-011, NFR-001, NFR-007 map here.

## Subtasks

- Implement `cache.Cache` keyed by stable hash of `(kind, locator)` (reuse WP01's redaction-safe ID helper).
- Store `Secret` handles, not raw bytes; on eviction (TTL expiry, manual invalidation, capacity), call `Secret.Destroy()`.
- Implement default TTL 60s with per-`BackendKind` overrides supplied at registration time.
- Implement `Invalidate(ref CredentialReference)` API on the cache and surface it through `Resolver.Invalidate(ref)` (resolver wiring lands fully when the package-level Resolver assembles in WP06+; for now expose the cache method).
- Implement a backend-driven invalidation hook: a backend can publish a rotation signal (channel or callback) that the cache listens to and acts on (mock implementation; real KMS rotation events are tested in WP11).
- Add a microbenchmark validating warm-cache p99 < 1 ms (NFR-001).
- Unit tests for TTL expiry, manual invalidation, eviction-triggered Destroy, concurrent access (goroutine race tests with `-race`).

## Acceptance criteria

- `core/secrets/cache/cache.go` compiles and is used by the resolver (final wiring in WP06).
- Eviction always zeroizes via `Secret.Destroy()`.
- Default TTL is 60s; per-backend override demonstrated by test.
- Microbenchmark records warm-cache p99 < 1 ms on developer-grade hardware.
- Concurrent access is race-free under `go test -race`.
- Tests achieve ≥80% line coverage on `core/secrets/cache/`.
- Charter quality gates pass.

## Files to create / modify

- Create `core/secrets/cache/cache.go`.
- Create `core/secrets/cache/cache_test.go`.
- Create `core/secrets/cache/cache_bench_test.go`.

## Definition of done

- WP02 (registry) and WP03 (Secret) dependencies satisfied.
- FR-010, FR-011, NFR-001 acceptance scenarios traceable to tests/benchmarks in this WP.
- Eviction zeroizes (no leftover bytes after expiry).
- Handoff: cache is ready to be inserted into the Resolver assembled by WP06.
