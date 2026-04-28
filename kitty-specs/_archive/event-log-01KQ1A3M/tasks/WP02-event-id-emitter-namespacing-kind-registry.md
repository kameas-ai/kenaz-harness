---
work_package_id: "WP02"
title: "ULID event ids, emitter id namespacing, and open kind registry"
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
  - "T005"
phase: "Phase 1 - Identity"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP02 – ULID event ids, emitter id namespacing, and open kind registry

## Goal

Implement the identity primitives every event entry depends on:
monotonic-within-process ULID generation, the namespaced `EmitterID`
allowlist, and the open `Kind` registry that supports forward
compatibility (NFR-006). This WP supplies the validated inputs the
append path (WP04) consumes.

## Spec references

- FR-002 — Typed event kinds with stable, forward-compat shapes.
- FR-016 — Globally unique, monotonically ordered id (ULID).
- FR-017 — Emitter id namespacing (`llm/`, `mcp/client`, `mcp/server`,
  `a2a/`, `scheduler/`, `bundle/`, `trust/`, `context/`, `session/`,
  `event-log/`, `storage/`).
- NFR-006 — Older readers preserve and pass through unknown event kinds
  without erroring.
- SC-006 — Adding a new emitter / new kind requires no change to other
  emitters or non-handling consumers.

## Plan references

- §3 Public API — `EmitterID`, `Kind`, `ULID` types.
- §5.6 Emitter id namespacing — allowlisted prefixes.
- §2 (`kind/registry.go`) — open kind registry; consumers register their
  own; unknown kinds preserved.
- Risk R7 — forward-compat unknown-kind drift mitigation.

## Subtasks

- T001 — Implement `core/event/internal/idgen` (or equivalent
  unexported subpackage) with a process-monotonic ULID generator;
  guarantee strict monotonicity within a process even when system time
  jitters; expose only via `core/event` constructors so callers cannot
  forge ids.
- T002 — Implement `EmitterID` validation: parse against the allowlisted
  prefix set documented in plan §5.6; return `ErrUnknownEmitter` for any
  other prefix; expose `RegisteredEmitterPrefixes() []string` for tests.
- T003 — Implement `core/event/kind/registry.go`: open registry of
  built-in kinds for self-events (`event-log.retention.started`,
  `event-log.retention.completed`, `event-log.redaction.salt-rotated`,
  `event-log.session.branched`); kinds from external namespaces are
  accepted as opaque and preserved verbatim (NFR-006); registry exposes
  `IsRegistered(Kind) bool` and `Register(Kind)` (process-local; idempotent).
- T004 — Document the kind-name grammar (`<namespace>.<dotted.path>`),
  enforce well-formedness at validation, and reject malformed kinds with
  `ErrInvalidKind`.
- T005 — Property test the ULID generator: 1M ids in a tight loop; assert
  strict lexicographic monotonicity, no collisions, and parseability.

## Acceptance criteria

- ULID generation: 1M-id property test passes with zero collisions and
  zero monotonicity violations under `go test -race`.
- `EmitterID` validation: table-driven tests cover every allowlisted
  prefix accepting; non-allowlisted prefixes rejected with
  `ErrUnknownEmitter`.
- Kind registry: built-in self-event kinds registered; an unknown kind
  from a `mcp/server` emitter is accepted as opaque and preserved
  verbatim (asserted by round-tripping it through validation).
- Adding a new emitter prefix to the allowlist requires no change to
  unrelated tests (SC-006 surrogate assertion).
- `go test ./core/event/...` and `go test -race ./core/event/...` green;
  `go vet` clean; `golangci-lint run` clean.

## Files to create / modify

- `core/event/internal/idgen/ulid.go`
- `core/event/internal/idgen/ulid_test.go`
- `core/event/emitter_id.go`
- `core/event/emitter_id_test.go`
- `core/event/kind/registry.go`
- `core/event/kind/registry_test.go`

## Definition of done

- All subtasks complete; tests green under `-race`.
- `go vet` and `golangci-lint run` clean.
- ADR drafted (or referenced) for the per-process monotonic ULID choice
  if this departs from a vanilla library default (DIRECTIVE_003).
