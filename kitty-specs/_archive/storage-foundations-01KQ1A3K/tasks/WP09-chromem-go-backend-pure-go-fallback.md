---
work_package_id: "WP09"
title: "chromem-go vector backend (pure-Go fallback, build-tag)"
dependencies:
  - "WP07"
planning_base_branch: "feat/storage-foundations-01KQ1A3K"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on feat/storage-foundations-01KQ1A3K; completed changes must merge back into main via squash merge per charter Branch Strategy."
subtasks:
  - "T001: add chromem-go dependency under build tag harness_chromem"
  - "T002: implement Collection interface against chromem-go"
  - "T003: pass backend-neutral contract suite under harness_chromem"
  - "T004: document CGo-free build path and corpus-size guidance"
  - "T005: <=100k smoke bench"
phase: "Phase 9 - chromem-go pure-Go fallback"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP09 – chromem-go vector backend (pure-Go fallback, build-tag)

## Goal

Add chromem-go as the pure-Go, CGo-free vector backend behind the `harness_chromem` build tag, suitable for tests, CI environments, and operators who insist on no-CGo binaries. Suitable for corpora <=100k. Reuse the WP07 contract suite.

## Spec references

- FR-007 (pluggable vector backends — chromem-go pure-Go fallback)
- C-001, C-006 (extensibility)
- SC-005 (boundary preservation)

## Plan references

- §2 vector/chromem/ with build tag harness_chromem
- §7 v1.x adds chromem-go
- Research D4 (E11)

## Subtasks

1. Add `philippgille/chromem-go` to `go.mod` under build constraint `//go:build harness_chromem`. Pure-Go: no CGo, no native libs.
2. Implement `core/storage/vector/chromem/{store.go,collection.go,doc.go}` against the `Collection` interface. Map metrics, persist collection files under the harness data dir alongside `data.db` (separate path tree under `vector/chromem/<collection>/`).
3. Implement `Reindex` by walking a consumer-supplied iterator and rebuilding the chromem index.
4. Run the WP07 contract suite under `go test -tags harness_chromem ./core/storage/vector/...`. All green.
5. Smoke bench: 100k docs, k=10, latency reported (research baseline ~40 ms; document our run for comparison). Hard guidance comment: "Not appropriate for >100k vectors."
6. Runtime guard: selecting `BackendChromem` without the build tag returns `ErrBackendNotCompiled`.

## Acceptance criteria

- Build under `-tags harness_chromem` compiles without CGo (`CGO_ENABLED=0` works).
- Default build is unchanged.
- Contract suite passes under the tag.
- 100k smoke bench recorded.
- `BackendChromem` selection without the tag returns `ErrBackendNotCompiled`.
- No files outside `core/storage/vector/chromem/` modified beyond the enum value.

## Files to create/modify

- Create: `core/storage/vector/chromem/{store.go,collection.go,doc.go,store_test.go}`
- Modify: `core/storage/vector/vector.go` (BackendChromem enum)
- Modify: `go.mod` (chromem-go under build tag)

## Definition of done

- chromem-go backend works under build tag, CGo-free, passes contract suite.
- Documentation flags the corpus-size ceiling.
- Default build remains untouched.
