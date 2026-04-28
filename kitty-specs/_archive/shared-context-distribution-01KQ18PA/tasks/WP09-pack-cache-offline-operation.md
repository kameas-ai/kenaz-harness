---
work_package_id: "WP09"
title: "Pack cache layer and offline cache-only operation"
dependencies:
  - "WP02"
  - "WP06"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
phase: "Phase 9 - Cache + offline"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP09 – Pack cache + offline operation

## Goal

Implement the local-first context-pack cache rooted under the project data directory (`cache/context/sha256/<digest>/`) layered on top of `core/bundle/`'s content-addressable cache, plus the `cache-only` resolution mode that lets the harness operate with zero outbound network egress when the lockfile-pinned hashes are present locally. This is what makes the charter's local-first invariant honest for context.

## Spec references

- FR-007 (Offline / cache-only operation)
- NFR-004 (Offline availability — zero outbound egress with valid cache)
- C-007 (No covert network egress; only configured channels, only during scheduled passes)
- Acceptance scenarios US6.1, US6.2 (cache-only resolution, scheduled-pass skip)

## Plan references

- §4.5 (Pack cache responsibilities; reuse of `core/bundle/` content-addressable cache)
- Risk R3 (cache staleness vs replay determinism — snapshot store from WP06 is canonical for replay)
- §7 v1.0 (Offline / cache-only operation)
- Edge case: "distribution URL becomes unreachable → continue serving cache, log warning"

## Subtasks

- T001 Implement `core/context/cache/pack_cache.go` building on `core/bundle/`'s content-addressable cache; add context-pack-specific indexes (lookup by name + version + layer for fast layer composition).
- T002 Wire `cache-only` resolution mode: when the resolver detects no network or operator-requested offline, serve every lockfile-pinned hash from cache; if any hash is missing, return a typed `ErrCacheMiss`.
- T003 Conservative GC (parallels `core/bundle/` FR-011): keep cache entries across pack removal so re-installs are fast; expose a manual purge API.
- T004 Integration test: after a fresh resolution + cache, disable network access; resolution succeeds; lockfile-pinned hash missing from cache yields typed cache-miss error.

## Acceptance criteria

- With network disabled and a populated cache, resolution succeeds and the snapshot's `Mode` is `cache-only`.
- Missing lockfile-pinned hash with no network produces a typed cache-miss error rather than a silent fallback (C-007).
- No outbound network calls during steady-state operation — only during scheduled resolver passes or operator-initiated updates.
- ≥80 % coverage; charter-required black-box integration test for cache behavior.

## Files to create/modify

- `core/context/cache/pack_cache.go`
- `core/context/cache/offline.go`
- `core/context/cache/pack_cache_test.go`
- `core/context/cache/testdata/...`

## Definition of done

- Offline integration test passes (network disabled at OS level via test harness).
- Cache layout is content-addressable under the project data directory.
- WP merged to main via squash-merge PR.
