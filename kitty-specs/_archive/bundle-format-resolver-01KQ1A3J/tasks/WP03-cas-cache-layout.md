---
work_package_id: "WP03"
title: "Content-addressable storage (CAS) layout under data_dir/cache/sha256/"
dependencies: []
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
phase: "Phase 3 - Cache"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP03 – CAS layout under data_dir/cache/sha256/aa/bb/

## Goal

Build the on-disk content-addressable cache that survives bundle removal, supports atomic writes after verification, and serves the local-first invariant (zero network on warm cache).

## Spec references

- FR-011 Local cache management
- FR-015 Cancellation safety (staging directory deleted on cancel; canonical path never holds unverified bytes)
- NFR-005 Local-first operation (warm cache resolves with zero outbound network)
- NFR-006 Memory ceiling (streaming I/O to keep peak under 200 MB)
- C-004 Local-first invariant
- Edge case: filesystem case-sensitivity / path collisions across OS targets.

## Plan references

- Plan §2 stub migration `store.go → core/bundle/cache/cas.go`
- Plan §4.3 step 5 (CAS write succeeds only after verification; staging path never under canonical `sha256/...`)
- Plan §4.6 Cache evictor (LRU + TTL, indefinite default)
- Plan §5.3 CAS layout (`<data_dir>/cache/sha256/<aa>/<bb>/<digest>`, staging, manifests)
- Plan §6.1 storage-foundations integration (request data-dir path; treat as opaque storage)
- Plan §8 R9 (filesystem case-sensitivity — two-hex-char nesting is collision-safe)

## Cross-mission dependencies

- **storage-foundations** (FR-001): supplies `<project_data_dir>` path. Until that mission lands, this WP must accept the data dir via constructor option (`WithDataDir`) and document the contract.

## Subtasks

- T001 Define the on-disk layout exactly per Plan §5.3 (`sha256/aa/bb/<digest>`, `staging/<random>`, `manifests/sha256/...`).
- T002 Implement `CAS` interface: `Has(digest string) bool`, `Get(digest string) (io.ReadCloser, error)`, `Put(reader io.Reader, expected string) (Receipt, error)` where Put streams to staging, computes SHA-256, compares to `expected`, atomically renames into the canonical path on match, deletes staging on mismatch (returns `ErrIntegrityMismatch`).
- T003 Make `Put` cancellation-safe: on `ctx.Done()`, partial staging file is deleted before returning `ErrCancelled`.
- T004 Implement `Evict(digest string)` and `GC(policy EvictPolicy)` for LRU + TTL; default policy is "indefinite" (never auto-evict).
- T005 Add a separate `manifests/` sub-CAS keyed by manifest content hash (parsed-and-validated manifest cache distinct from raw artifact bytes).
- T006 Path-collision tests: 10k random digests across nested `aa/bb/` — verify no collisions; confirm correct behavior on case-insensitive filesystems (macOS HFS+, Windows NTFS).

## Acceptance criteria

- A successful `Put` lands bytes at `<data_dir>/cache/sha256/<aa>/<bb>/<digest>` only after hash verification.
- A mismatched-hash `Put` deletes the staging file and returns `ErrIntegrityMismatch`; canonical path is never touched.
- Cancellation mid-`Put` deletes the staging file and returns `ErrCancelled`.
- `Has` for an evicted digest returns `false`; `Get` returns a not-found error.
- `manifests/` sub-CAS is independent of `sha256/` artifact CAS.
- All file operations use streaming I/O (no full-buffer reads).

## Files to create/modify

- `core/bundle/cache/cas.go` (new — CAS interface + impl)
- `core/bundle/cache/layout.go` (new — path computation, atomic rename helpers)
- `core/bundle/cache/evict.go` (new — LRU/TTL policies, GC)
- `core/bundle/cache/manifests.go` (new — parsed-manifest CAS)
- `core/bundle/errors.go` (extend — `ErrIntegrityMismatch`, `ErrCancelled`)
- Migrate the stub `core/bundle/store.go` (Plan §2).

## Definition of done

- All acceptance criteria covered by unit tests including a fuzz test on `Put` with random byte streams and forced cancellation.
- No imports from `manifest/`, `lockfile/`, `resolver/`, or `channels/`.
- CAS path layout documented in package doc comment.
- Cross-OS path tests pass (or are clearly gated for CI matrix).
