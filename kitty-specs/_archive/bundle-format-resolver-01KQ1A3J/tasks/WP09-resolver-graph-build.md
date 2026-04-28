---
work_package_id: "WP09"
title: "Resolver graph build and deterministic ordering"
dependencies:
  - "WP01"
  - "WP02"
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
phase: "Phase 9 - Resolver"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP09 – Resolver graph build and deterministic ordering

## Goal

Build the resolver core: pre-flight validation, top-level config walk, dependency graph construction with cycle and duplicate detection, layered composition (team → personal), conflict detection with declared precedence, and deterministic activation order. This WP wires WP01–WP05 together but defers fetch/verify (WP10) and activation (WP11) to subsequent WPs.

## Spec references

- FR-005 Deterministic resolution
- FR-008 Layered composition (team + personal)
- FR-009 Conflict detection
- FR-012 Pre-flight validation
- FR-016 Resolver progress reporting (skeleton hooks; concrete reporter wiring is in WP12)
- NFR-001 Warm-cache resolution latency under 500 ms p95
- NFR-002 Determinism rate 100%
- C-001 Architectural integrity
- US2 (P1) team + personal compose deterministically; pinned hash beats new published version.

## Plan references

- Plan §2 `core/bundle/resolver/` subpackage
- Plan §3.2 Resolver API (Resolve, Activate, Verify, Remove + ResolvedGraph type)
- Plan §4.1–4.2 Pre-flight + graph builder
- Plan §4.5 Conflict detector (last-layer-wins default; `overrides:` per artifact)
- Plan §8 R7 (perf regression — record per-phase timing in ResolutionMeta)

## Subtasks

- T001 Define `ResolvedGraph`, `ResolvedBundle`, `ArtifactRef`, `ResolutionMeta` types matching Plan §3.2.
- T002 Implement pre-flight: parse top-level config + lockfile; validate lockfile schema_version; resolve channel specs via WP05 registry; call `Channel.Reachable` for each; `secrets.Resolver.Lookup(ref)` for credential refs.
- T003 Implement graph builder: walk top-level config in declared order; for each `BundleReference`, resolve to `(name, version)` using lockfile pins (authoritative) or semver range against the manifest; recurse into transitive dependencies; detect cycles (`ErrCyclicDependency`) and duplicates (`ErrDuplicateArtifact`).
- T004 Implement deterministic activation order: stable topological sort with byte-wise tie-breaker on `(name, version, channel_url)`. Two runs on identical inputs MUST produce identical orders.
- T005 Implement conflict detector: two activations of the same `(kind, name)` across bundles trigger conflict resolution per declared precedence; default policy is last-layer-wins (personal > team); `overrides:` array in `kaneaz.yaml` allows per-conflict policy; ambiguous case yields `ErrConflictUnresolved`.
- T006 Wire `Resolver.Resolve(ctx, cfg, lock)` to call pre-flight + graph build + conflict detection, returning a `*ResolvedGraph` with `SnapshotID` (ULID), `ContentHash` (SHA-256 of canonical serialization), and `ResolutionMeta` (per-phase timing, channel cache hit/miss placeholders).
- T007 Determinism harness: run resolve twice in the same test, assert byte-identical `ResolvedGraph.ContentHash`. Run on Linux + macOS in CI to catch sort-locale issues.

## Acceptance criteria

- Identical inputs produce byte-identical `ResolvedGraph` (NFR-002 SC-002).
- Lockfile pin overrides a newer published version (US2 acceptance scenario 3).
- A circular dependency returns `ErrCyclicDependency` with the full cycle path.
- Personal-over-team conflict resolves to personal; the displaced artifact is recorded in `ResolutionMeta`.
- An ambiguous conflict yields `ErrConflictUnresolved` and aborts before activation.
- Pre-flight surfaces `ErrChannelUnreachable` for any unreachable channel whose artifacts are not in cache.

## Files to create/modify

- `core/bundle/resolver/resolver.go` (new — Resolver impl skeleton)
- `core/bundle/resolver/graph.go` (new — graph build, cycle detection)
- `core/bundle/resolver/plan.go` (new — ResolvedGraph type + canonical serialization)
- `core/bundle/resolver/conflicts.go` (new — conflict detection + precedence)
- `core/bundle/resolver/preflight.go` (new — channel reachability + cred lookups)
- `core/bundle/errors.go` (extend — `ErrCyclicDependency`, `ErrConflictUnresolved`, `ErrPinnedArtifactMissing`)
- Migrate the stub `core/bundle/resolver.go` (Plan §2).

## Definition of done

- All acceptance criteria pass.
- Determinism golden test: two runs produce identical `ContentHash` on Linux and macOS.
- No imports from `channels/git`, `channels/oci`, `channels/http`, or any concrete kind handler — only `channels`, `kinds`, `manifest`, `lockfile`, `cache`, `errors`.
- `Resolver.Resolve` returns a `*ResolvedGraph` ready to feed into WP10 (verify) and WP11 (activate).
