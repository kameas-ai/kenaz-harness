---
work_package_id: "WP08"
title: "LanceDB vector backend (opt-in, build-tag)"
dependencies:
  - "WP07"
planning_base_branch: "feat/storage-foundations-01KQ1A3K"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on feat/storage-foundations-01KQ1A3K; completed changes must merge back into main via squash merge per charter Branch Strategy."
subtasks:
  - "T001: add lancedb-go dependency under build tag harness_lancedb"
  - "T002: implement Collection interface against LanceDB"
  - "T003: pass backend-neutral contract suite under harness_lancedb"
  - "T004: document CGo + native lib build steps and prebuilt-binary expectations"
  - "T005: bench scaling beyond 500k vectors (smoke run; gated)"
phase: "Phase 8 - LanceDB backend"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP08 – LanceDB vector backend (opt-in, build-tag)

## Goal

Add LanceDB as an opt-in vector backend behind the `harness_lancedb` build tag, satisfying the >500k-vector workload path identified in research D3. Reuse the WP07 contract test suite so the backend swap is provably interface-only.

## Spec references

- FR-007 (pluggable vector backends — LanceDB)
- C-001, C-006 (extensibility — adding a backend requires no changes outside its directory)
- SC-005 (new vector backend added end-to-end without modifying any `core/` package outside the new backend's directory)
- US 2 acceptance scenario 2 (alternative backend holds the same API contract)

## Plan references

- §2 vector/lancedb/ with build tag harness_lancedb
- §7 v1.x adds LanceDB
- §8 R7 (LanceDB Go bindings v0.1.2 — early stage; opt-in only)
- Research D3 (E9, E10)

## Subtasks

1. Add `lancedb/lancedb-go` (or equivalent current binding) to `go.mod` under build constraint `//go:build harness_lancedb`. Document the prebuilt-native-lib + env-var build steps in `core/storage/vector/lancedb/BUILD.md` (research notes "CGo + env-var build complexity").
2. Implement `core/storage/vector/lancedb/{store.go,collection.go,doc.go}` against the `Collection` interface from WP07. Map metric (cosine/l2/dot) to LanceDB's distance functions. Quantization=none for v1.
3. Implement `Reindex` against LanceDB's index rebuild API; emit `vector_reindex_completed`.
4. Run the WP07 backend-neutral contract test suite under `go test -tags harness_lancedb ./core/storage/vector/...`. All tests pass without modification.
5. Smoke bench: 500k vectors, k=10, p95 latency reported (no hard gate yet — research note: "reassess at v1.x"). Output saved as evidence in PR.
6. Add a runtime guard: if `cfg.VectorBackend == BackendLanceDB` is selected but the binary was built without `harness_lancedb`, return `ErrBackendNotCompiled` with actionable instructions.

## Acceptance criteria

- Build under `-tags harness_lancedb` compiles and links.
- Default build (without the tag) still compiles, has zero LanceDB imports, and selecting `BackendLanceDB` returns `ErrBackendNotCompiled`.
- Contract test suite green under the tag.
- 500k-vector smoke bench results recorded.
- No file outside `core/storage/vector/lancedb/` is changed beyond the `BackendLanceDB` enum value, the `ErrBackendNotCompiled` constant, and the optional registration hook in `core/storage/vector/vector.go`.

## Files to create/modify

- Create: `core/storage/vector/lancedb/{store.go,collection.go,doc.go,BUILD.md,store_test.go}`
- Modify: `core/storage/vector/vector.go` (BackendLanceDB enum + ErrBackendNotCompiled)
- Modify: `go.mod` (lancedb-go under build tag)

## Definition of done

- LanceDB backend works under build tag and passes the shared contract suite.
- Build instructions documented; no impact on default build.
- SC-005 (boundary preservation) demonstrably true via diff inspection.
