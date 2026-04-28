---
work_package_id: "WP06"
title: "Git distribution channel (HTTPS / SSH refs)"
dependencies:
  - "WP05"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
phase: "Phase 6 - Channels (git)"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP06 – Git distribution channel

## Goal

Implement the `git` distribution-channel adapter: fetch bundles from a git repository at a pinned ref (commit, tag) over HTTPS or SSH, with credential resolution via the secrets-keychain reference machinery.

## Spec references

- FR-003 Distribution-channel abstraction (git is a day-one kind)
- FR-012 Pre-flight validation
- C-002 No covert network egress (only configured channels, only when fetching)
- C-004 Local-first invariant (git fetch only on cache miss)

## Plan references

- Plan §2 `core/bundle/channels/git/` subpackage (go-git)
- Plan §3.4 Channel interface
- Plan §4.3 Fetch pipeline (cache lookup → channel fetch → verify → CAS write)
- Plan §6.2 secrets-keychain integration (auth via `secrets.Resolver`)

## Cross-mission dependencies

- **secrets-keychain** (FR-001): credential references for HTTPS PATs and SSH keys.

## Subtasks

- T001 Add `github.com/go-git/go-git/v5` (or vetted equivalent) to `go.mod`.
- T002 Implement `git.Channel.Reachable(ctx)`: a shallow `ls-remote` against the configured remote with auth resolved through `secrets.Resolver`. Return `ErrChannelUnreachable` on network or auth failure.
- T003 Implement `git.Channel.Fetch(ctx, coord, sink)`: clone shallow at the pinned ref into a temp dir, locate `coord.Path` within the working tree, stream bytes to sink, clean up. Pinned ref is required (commit SHA or annotated tag); branch refs are rejected (non-deterministic).
- T004 Implement `git.Channel.LookupSignatures`: locate sibling `.sig` files for the artifact and return matching `SignatureRef` values.
- T005 Credential handling: HTTPS uses PAT or basic auth via `secrets.Resolver.Resolve(ref)` only at fetch time; SSH uses a key path resolved from a keychain ref. Never log the resolved value.
- T006 Cancellation: `ctx.Done()` aborts the in-flight clone and removes the temp dir; return `ErrCancelled`.

## Acceptance criteria

- Integration test against a local fixture git repo (or `git daemon`) successfully fetches an artifact at a pinned commit SHA.
- Branch ref (e.g., `refs/heads/main`) is rejected with a structured error citing determinism.
- Network failure returns `ErrChannelUnreachable` with the endpoint redacted of credentials.
- Cancellation mid-clone deletes the temp dir.
- Credential resolution happens once per fetch and is never recorded in events or logs.

## Files to create/modify

- `core/bundle/channels/git/git.go` (new — Channel impl)
- `core/bundle/channels/git/auth.go` (new — credential resolution helpers)
- `go.mod`, `go.sum` (add go-git)

## Definition of done

- All acceptance criteria pass via integration test using a local git fixture.
- Package imports only `channels`, `cache`, `errors`, `secrets` (interface), and the go-git library.
- Pinned-ref enforcement documented in package doc.
- Credential redaction verified — manual review of any debug logging added.
