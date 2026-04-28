---
work_package_id: "WP05"
title: "Channel contract and local_path channel"
dependencies:
  - "WP03"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
phase: "Phase 5 - Channels (local_path)"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP05 – Channel contract and local_path channel

## Goal

Define the pluggable distribution-channel abstraction (`channels.Channel` interface + `channels.Registry`) and ship the simplest concrete adapter — `local_path` — that reads bundles from a filesystem path. This is the seam every other channel kind plugs into.

## Spec references

- FR-003 Distribution-channel abstraction (one of four day-one channels)
- FR-012 Pre-flight validation (`Channel.Reachable`)
- NFR-007 Channel-contract extensibility
- C-001 Architectural integrity (channels live in their own subpackages)
- C-004 Local-first invariant

## Plan references

- Plan §2 `core/bundle/channels/` package layout
- Plan §3.4 Channel interface (Kind, Reachable, Fetch, LookupSignatures)
- Plan §4.1 Pre-flight validator (calls Reachable on every channel spec)
- Plan §6.2 secrets-keychain integration (channel auth via `secrets.Resolver`; local_path requires no creds)

## Cross-mission dependencies

- **secrets-keychain**: `secrets.Resolver` interface is consumed by the registry's `Open` factory. local_path channel does not call it, but the signature must be in place.

## Subtasks

- T001 Define `Channel` interface: `Kind() string`, `Reachable(ctx) error`, `Fetch(ctx, ref ArtifactCoord, sink io.Writer) (FetchResult, error)`, `LookupSignatures(ctx, ref) ([]SignatureRef, error)`.
- T002 Define `channels.Registry` interface: `Register(kind string, factory Factory) error`, `Open(spec ChannelSpec, creds secrets.Resolver) (Channel, error)`. Implement concurrency-safe default registry.
- T003 Define value types: `ChannelSpec`, `ArtifactCoord`, `FetchResult`, `SignatureRef`, `Factory func(ChannelSpec, secrets.Resolver) (Channel, error)`.
- T004 Implement `channels/localpath/localpath.go`: `Reachable` checks the path exists and is readable; `Fetch` streams bytes from `<root>/<artifact-relative-path>`; `LookupSignatures` looks for sibling `.sig` files (preview of Ed25519 detached path used by WP10).
- T005 Path-traversal hardening on `Fetch`: confirm `ArtifactCoord` resolves to a path inside the configured root (`filepath.Rel` + `..` rejection); return `ErrPathTraversal` on escape.

## Acceptance criteria

- An integration test wires a local_path channel pointing at a fixture bundle directory, calls `Reachable` (pass), and `Fetch` for a known artifact, streaming bytes to a sink.
- An `ArtifactCoord` containing `..` returns `ErrPathTraversal` without ever opening the file.
- `Reachable` on a non-existent root returns `ErrChannelUnreachable`.
- Registering two channels with the same kind returns an error.
- `LookupSignatures` returns `[]SignatureRef{}` (not error) when no `.sig` is present.

## Files to create/modify

- `core/bundle/channels/channel.go` (new — interface, value types)
- `core/bundle/channels/registry.go` (new — registry impl)
- `core/bundle/channels/localpath/localpath.go` (new — local_path adapter)
- `core/bundle/errors.go` (extend — `ErrChannelUnreachable`, `ErrChannelUnknown`)

## Definition of done

- All acceptance criteria pass.
- `channels/` package compiles with no dependency on resolver, manifest, or lockfile (cache + errors only).
- Path-traversal regression test present.
- Public API matches Plan §3.4 exactly.
- Registry seam is the *only* extension point — confirmed by package doc and import graph.
