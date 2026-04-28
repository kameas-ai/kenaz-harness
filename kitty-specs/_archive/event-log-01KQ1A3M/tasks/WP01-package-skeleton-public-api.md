---
work_package_id: "WP01"
title: "Package skeleton and public API surface in core/event"
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
phase: "Phase 1 - Core skeleton"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP01 – Package skeleton and public API surface in core/event

## Goal

Establish `core/event/` as the single architectural home of the event-log
substrate (DIRECTIVE_001 / C-001). Materialize the canonical, illustrative
Go types and interfaces (`Emitter`, `Reader`, `Verifier`, `Replayer`,
`Brancher`, `Retention`, `Event`, `Kind`, `EmitterID`, `AppendInput`,
`ReadOpts`, `ReplayOpts`, `Cursor`, `Report`, `RedactionSummary`) and the
typed error taxonomy on which every other WP and every consuming mission
depends. No internal logic yet — this WP is the seam.

## Spec references

- FR-001 — Single shared event-log surface.
- FR-002 — Typed event kinds with stable, forward-compat shapes.
- FR-003 — Append-only enforcement at the public API (no `Update` / `Delete`).
- FR-016 — Stable event id (ULID).
- FR-017 — Emitter id namespacing.
- FR-018 — Uniform shape for cancel / error / timeout events.
- C-001 — All event-log logic in `core/event/`; package public seam is the
  single emit/read/verify surface.
- C-002 — Append-only at storage boundary; mutation paths not exposed.
- C-004 — Redaction non-bypassable; Emitter is the only ingress.

## Plan references

- §2 Architectural placement — directory tree under `core/event/`.
- §3 Public API — illustrative Go signatures this WP materializes.
- §6.3 Every emitter — helper constructors `event.Cancellation`,
  `event.Error`, `event.Timeout` for FR-018.

## Subtasks

- T001 — Create `core/event/` directory tree (`api.go`, `errors.go`,
  `doc.go`, plus empty subpackage skeletons for `log/`, `redact/`,
  `chain/`, `replay/`, `branch/`, `retention/`, `kind/` — each with
  `doc.go` referencing the boundary invariant).
- T002 — Define `EmitterID`, `Kind`, `ULID`, `Event` struct in
  `core/event/api.go`; define `AppendInput`, `ReadOpts`, `ReplayOpts`,
  `Cursor`, `ReplayCursor`, `Report`, `RedactionSummary`, `ContentQuery`,
  `Selector`, `Policy`, `RetentionReport`, `ArchiveRef`, `TruncateReport`,
  `Archive`.
- T003 — Define interfaces `Emitter`, `Reader`, `Verifier`, `Replayer`,
  `Brancher`, `Retention`. Confirm the package exports no `Update`,
  `Delete`, `Patch`, raw `*sql.DB`, or internal store pointer.
- T004 — Define typed error taxonomy in `core/event/errors.go`:
  `ErrChainBroken`, `ErrRedactionBypassed`, `ErrUnknownEmitter`,
  `ErrInvalidKind`, `ErrTruncated`, `ErrAppendOnly`, `ErrPolicyViolation`,
  `ErrSessionNotFound`, `ErrAlreadyArchived`, with classification helpers
  (e.g., `IsAppendOnlyViolation(err) bool`).
- T005 — Define FR-018 helper constructors `Cancellation(ctx, info) Event`,
  `Error(ctx, err) Event`, `Timeout(ctx, info) Event` returning
  pre-shaped `AppendInput` values (not yet appended; consumers pass to
  `Emitter.Append`).

## Acceptance criteria

- `go build ./core/event/...` succeeds; package compiles.
- `go vet ./core/event/...` clean; `golangci-lint run ./core/event/...`
  clean.
- Table-driven unit tests in `core/event/api_test.go` and
  `core/event/errors_test.go` cover (a) error classification helpers,
  (b) `EmitterID` namespace allowlist round-trip, (c) helper constructor
  shapes for cancel/error/timeout (FR-018).
- No file under `core/event/` imports `database/sql`, `libsql`, Wails,
  or any frontend package (DIRECTIVE_001).
- Public surface contains no exported function or method that mutates
  or deletes existing events (FR-003 / C-002 verified by
  `go doc ./core/event` review).

## Files to create / modify

- `core/event/api.go`
- `core/event/errors.go`
- `core/event/doc.go`
- `core/event/api_test.go`
- `core/event/errors_test.go`
- `core/event/log/doc.go`
- `core/event/redact/doc.go`
- `core/event/chain/doc.go`
- `core/event/replay/doc.go`
- `core/event/branch/doc.go`
- `core/event/retention/doc.go`
- `core/event/kind/doc.go`

## Definition of done

- All subtasks complete; tests green; `go vet` and `golangci-lint run`
  clean for `./core/event/...`.
- Public surface aligns with plan §3 signatures; deviations recorded in
  commit body or ADR per DIRECTIVE_003.
- PR opened against `feat/event-log-01KQ1A3M` targeting `main`,
  ≥ 1 maintainer approval, squash-merge per charter Branch Strategy.
