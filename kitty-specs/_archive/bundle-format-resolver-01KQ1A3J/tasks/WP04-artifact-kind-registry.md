---
work_package_id: "WP04"
title: "Artifact-kind registry contract (extension surface)"
dependencies:
  - "WP01"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
phase: "Phase 4 - Kinds"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP04 – Artifact-kind registry contract

## Goal

Define the `ArtifactKindHandler` interface and the `kinds.Registry` that lets new artifact kinds (provider profiles, MCP descriptors, hooks, context packs, agent definitions) plug in without modifying any other `core/` package. This is the harness's primary extension surface.

## Spec references

- FR-002 Artifact-kind registry
- US5 (P3) — new artifact kind added without core surgery
- SC-005 Measurable: new kind end-to-end with no commits to core packages outside the new handler
- NFR-007 Channel/kind extensibility
- C-001 Architectural integrity (DIRECTIVE_001)

## Plan references

- Plan §2 (`core/bundle/kinds/` subpackage)
- Plan §3.5 ArtifactKindHandler API (Kind, ParamSchema, Parse, Validate, Activate, Deactivate)
- Plan §4.4 Activation phase (registry dispatch)
- Plan §6 (concrete kind packages live in their own missions e.g. `core/llm/profilekind/`)

## Subtasks

- T001 Define `ArtifactKindHandler` interface exactly per Plan §3.5: `Kind() string`, `ParamSchema() []byte`, `Parse(ctx, src) (Parsed, error)`, `Validate(ctx, p) error`, `Activate(ctx, p, env) (Activation, error)`, `Deactivate(ctx, a) error`.
- T002 Define `kinds.Registry` interface with `Register(h ArtifactKindHandler) error`, `Lookup(kind string) (ArtifactKindHandler, bool)`, `List() []string`. Implement a concurrency-safe default registry; reject duplicate kind ids on `Register`.
- T003 Define supporting value types in `kinds/types.go`: `Parsed` (opaque), `Activation` (opaque), `Environment` (read-only context: data dir, secrets resolver, event emitter), `ArtifactSource` (bytes + manifest reference + bundle root path).
- T004 Provide a no-op test handler under `core/bundle/kinds/testkind/` (e.g., kind id `noop`) used by integration tests across this mission to prove the registry plumbing works without any concrete kind from a downstream mission.
- T005 Document the contract in package doc: stability commitments, what activations may and may not assume, ordering guarantees (resolver supplies stable order), the rule that handlers MUST NOT import `core/bundle/resolver/` or other handlers.

## Acceptance criteria

- The `noop` test handler registers and is dispatched by an integration test that resolves a bundle declaring an artifact of kind `noop`.
- Registering two handlers with the same `Kind()` returns an error.
- `Lookup` for an unregistered kind returns `(nil, false)`; the resolver consumer surfaces this as a typed `ErrUnknownArtifactKind`.
- `Environment` carries event emitter, secrets resolver, and data-dir path — and nothing else (locked-down surface).
- Registry concurrency-safe under parallel registrations (race detector clean).

## Files to create/modify

- `core/bundle/kinds/kind.go` (new — interface)
- `core/bundle/kinds/registry.go` (new — registry impl)
- `core/bundle/kinds/types.go` (new — Parsed, Activation, Environment, ArtifactSource)
- `core/bundle/kinds/testkind/noop.go` (new — test handler)
- `core/bundle/errors.go` (extend — `ErrUnknownArtifactKind`, `ErrDuplicateKindRegistration`)

## Definition of done

- Interface signatures match Plan §3.5 exactly.
- `noop` handler is exercised by a registry-only unit test and reused later by WP09/WP11 integration tests.
- No imports from `manifest/`, `lockfile/`, `resolver/`, `channels/`, or `cache/`.
- Package doc spells out the SC-005 contract: adding a kind requires zero changes outside its package.
