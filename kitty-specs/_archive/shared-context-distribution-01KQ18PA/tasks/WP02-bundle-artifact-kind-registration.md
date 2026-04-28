---
work_package_id: "WP02"
title: "Register context-pack artifact kind with bundle resolver"
dependencies:
  - "WP01"
  - "bundle-format-resolver-01KQ1A3J"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
phase: "Phase 2 - Bundle integration"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP02 – Register context-pack artifact kind

## Goal

Plug `context-pack` into the existing `core/bundle/` artifact-kind registry and channel contract so context packs flow through the same resolver, lockfile, and content-addressable cache as every other bundle artifact. No parallel distribution stack — context packs are a *kind*, not a peer system. This satisfies C-002 and inherits NFR-007 channel extensibility automatically.

## Spec references

- FR-004 (Distribution channel abstraction — git, oci, http_mirror via existing channel contract)
- FR-005 (Lockfile-pinned versions)
- C-001 (Logic confined to `core/context/`; channel SDKs never leak outside their package)
- C-002 (Bundle-format compatibility — context packs are a new kind, not a parallel surface)
- NFR-007 (Channel extensibility — adding a new channel touches only its own directory)
- SC-008 (Adding a new channel kind requires zero changes in `core/context/`)

## Plan references

- §2 (Boundary: `core/context/` consumes `core/bundle/` only via artifact-kind registry and channel contract)
- §4.1 (Pack ingester emits `ContextPack` for the kind handler)
- §5.2 (Lockfile entry shape: `kind = "context-pack"`, layer, content_hash, signature, required)
- §6 (Integration with `bundle-format-resolver-01KQ1A3J`)

## Subtasks

- T001 Implement `core/context/bundle_kind.go` registering a `context-pack` kind handler against the bundle artifact-kind registry; handler delegates parse to WP01's pack parser.
- T002 Define the lockfile entry projection (TOML fields per plan §5.2) and write/read through `core/bundle/`'s lockfile API only.
- T003 Wire the handler into the bundle resolver so `git`, `oci`, `http_mirror` channels can fetch a `context-pack` with no changes in `core/bundle/`.
- T004 Integration test: a fixture lockfile pinning a `context-pack` artifact resolves through a local OCI registry, content-hash-pinned, and emerges as a `ContextPack`.

## Acceptance criteria

- Bundle resolver treats `context-pack` as a first-class artifact kind; lockfile round-trip preserves all required fields.
- A fixture context-pack hosted on each of `git`, `oci`, `http_mirror` channels resolves successfully through `core/bundle/` with no `core/context/` channel code.
- Adding a new channel kind in `core/bundle/` (test fixture) makes the same `context-pack` available with zero edits in `core/context/` (validates SC-008).
- Black-box integration test added per charter (bundle resolver requires it).

## Files to create/modify

- `core/context/bundle_kind.go`
- `core/context/bundle_kind_test.go`
- `core/context/testdata/lockfile-context-pack.toml`
- Test harness extension under `core/context/internal/testfixtures/`

## Definition of done

- Integration test demonstrates lockfile-pinned context-pack resolution through every v1 channel (git, oci, http_mirror).
- Zero distribution-channel SDK imports in `core/context/`.
- Lockfile-write goes through bundle's API only.
- WP merged to main via squash-merge PR.
