---
work_package_id: "WP11"
title: "Retention substrate: keep_all default, archive, truncate"
dependencies:
  - "WP01"
  - "WP03"
  - "WP06"
  - "WP07"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
  - "T006"
phase: "Phase 10 - Retention"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP11 – Retention substrate: keep_all default, archive, truncate

## Goal

Implement `core/event/retention/` — the policy substrate, archive
operation (move events to a documented on-disk archive bundle), and
truncation operation (remove rows + emit truncation marker). Default
policy in v1.0 is `keep_all`; `keep_n_days` and `size_budget`
evaluators are wired but the scheduling driver lives in a follow-up
mission. Both archive and truncate emit `event-log.retention.started`
*before* and `event-log.retention.completed` *after* — the audit story
never silently loses data.

## Spec references

- FR-013 — Retention policy (keep_all default).
- FR-014 — Archive operation; documented archive format outside the
  active database.
- FR-015 — Truncation operation; logged before and recorded after.
- C-002 — Append-only at storage; truncation is an explicit, recorded
  operation.
- C-005 — SOC 2-readiness: retention is auditable.

## Plan references

- §2 (`retention/`) — `policy.go`, `archive.go`, `truncate.go`.
- §4.6 Retention path — six-step procedure with before/after envelope
  events.
- §5.4 `retention_config` table.
- §7 (v1.0 / v1.x split) — keep_all default in v1.0; scheduler in v1.x.
- Risk R8 — branch ancestor protection: refuse to truncate sessions
  that are ancestors of non-archived branches.

## Subtasks

- T001 — Implement `core/event/retention/policy.go`: load + version
  policies from `retention_config` table; evaluators for `keep_all`
  (no-op selector), `keep_n_days` (selector by `emitted_at` cutoff),
  `size_budget` (selector by aggregate row size). Default is
  `keep_all`.
- T002 — Implement `core/event/retention/archive.go`: serializes
  selected events to a documented JSON Lines bundle plus a chain
  manifest sidecar (chain heads + verification metadata). Archive is
  written outside the active DB; an `ArchiveRef` record links from
  `events`-side queries (so reader can return `ErrAlreadyArchived`
  per WP07).
- T003 — Implement `core/event/retention/truncate.go`: removes selected
  rows from `events`; emits a `chain.rebased` marker (Risk R2) so
  verifier handles the truncation point cleanly; refuses to truncate
  any session that is a parent of a non-archived branch (Risk R8
  pre-flight).
- T004 — Implement `Retention` interface from plan §3:
  `Apply(policy)`, `Archive(selector, dest)`, `Truncate(selector)`.
  Each operation appends `event-log.retention.started` *before* and
  `event-log.retention.completed` *after* through the WP06 emitter.
  Failure mid-op leaves the started event in place; the completed
  event is absent — operators can detect incomplete operations.
- T005 — Implement size-warning surface (FR-013 acceptance scenario 2):
  when policy is unset and the log exceeds a configurable threshold,
  surface a warning event `event-log.retention.size-warning`. No data
  is lost.
- T006 — Black-box integration tests: configure `keep_n_days = 7`,
  insert events spanning 30 days, run retention, assert (a) older
  events archived to bundle on disk, (b) bundle is parseable
  end-to-end, (c) `event-log.retention.started` and `completed` events
  appear in order in the active log, (d) reader returns
  `ErrAlreadyArchived` with archive reference for archived session
  queries.

## Acceptance criteria

- Default `keep_all` is in effect when no policy is configured (no
  data is removed; size warnings only).
- Archive bundle format is documented (JSON Lines + chain manifest);
  parseable by a documented reader.
- Truncation refuses to remove sessions that are ancestors of
  non-archived branches (Risk R8); test asserts the refusal.
- Started / completed envelope events appear in chain before / after
  any retention op (FR-014 / FR-015).
- Verifier handles the post-truncation chain cleanly (depends on WP08).
- `go test ./core/event/...` green; on-disk integration covered.

## Files to create / modify

- `core/event/retention/policy.go`
- `core/event/retention/archive.go`
- `core/event/retention/truncate.go`
- `core/event/retention/retention.go` (top-level wiring)
- `core/event/retention/retention_test.go`
- `core/event/retention/integration_test.go`
- `docs/event-log/archive-format.md` (documented archive format)

## Definition of done

- All subtasks complete; integration tests green; archive bundle
  documented under `docs/event-log/`.
- `go vet`, `golangci-lint run` clean.
- ADR optional — if archive bundle format diverges from JSON Lines +
  chain manifest sidecar, document via ADR (DIRECTIVE_003).
