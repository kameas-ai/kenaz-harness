---
work_package_id: "WP03"
title: "Transport contract, registry, and audit/secrets dependency facets"
dependencies:
  - "WP01"
  - "WP02"
planning_base_branch: "feat/wire-integration"
merge_target_branch: "feat/wire-integration"
branch_strategy: "Planning artifacts were generated on feat/mcp-plans; completed changes merge into feat/wire-integration."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
phase: "Phase 1 - Core skeleton"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP03 – Transport contract, registry, and audit/secrets dependency facets

## Goal

Materialize the transport-extensibility seam: the `Transport` interface,
the `TransportFactory` registry, the `TransportDeps` carrier with audit +
secrets sub-interfaces, and the `RegisterTransport` plumbing on the Pool.
Transport packages plug in here without modifying any other `core/`
package.

## Spec references

- FR-017 — Pluggable transport contract.
- FR-019 — Pre-flight credential resolution requires the secrets resolver
  facet exposed by `TransportDeps`.
- C-005 — Open-source / enterprise distribution split: third-party
  transports must register without forking core.

## Plan references

- §2 Architectural Placement — `core/mcp/client/transport/` lives at the
  seam.
- §3 Public API — `Transport`, `TransportFactory`, `TransportDeps`.
- §4 Internal Layering — connection state machine consumes
  `TransportFactory` indirectly via the registry.

## Subtasks

- T001 — Define `core/mcp/client/transport/transport.go`: the `Transport`
  interface (`Send`, `Recv`, `Close`); the `TransportFactory` type; the
  `TransportDeps` struct with `Secrets` (resolver façade) and `Audit`
  (emitter façade) fields.
- T002 — Define small inline interfaces for `Secrets` and `Audit` in
  `transport.go` so transports do not transitively import `core/secrets`
  or `core/event` directly. The `core/mcp/client` package adapts
  `core/secrets.Backend` to the `Secrets` facet and `core/event.Log` to
  the `Audit` facet.
- T003 — Implement an internal `core/mcp/client/transport/registry.go`
  with thread-safe `Register(kind string, factory TransportFactory)` and
  `Lookup(kind string) (TransportFactory, error)`. The package's default
  registry is a private singleton.
- T004 — Tests: register a fake transport, look it up, build it,
  exercise its `Send`/`Recv`/`Close`. Negative test: lookup of an
  unknown kind returns `ErrTransportUnknown`.

## Acceptance criteria

- `go build ./core/mcp/client/transport/...` succeeds.
- Coverage ≥ 80 %.
- The `Transport` interface has zero transport-specific dependencies.
- A fake transport in the test file successfully registers and is
  invoked end-to-end.
- `go list -deps ./core/mcp/client/transport | grep -v ^github.com/sigil-tech`
  returns only stdlib.

## Files to create / modify

- `core/mcp/client/transport/transport.go`
- `core/mcp/client/transport/registry.go`
- `core/mcp/client/transport/registry_test.go`
- `core/mcp/client/transport/doc.go`

## Definition of done

- All subtasks complete; tests green; lint clean.
- Registry exhibits race-free behavior under `go test -race`.
- PR merged into `feat/wire-integration`.
