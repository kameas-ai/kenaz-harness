---
work_package_id: "WP03"
title: "context_attachments table + Mission-A auto-migration"
dependencies:
  - "WP01"
  - "WP02"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
phase: "Phase 3 — Attachments + auto-migration"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-26T01:55:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP03 — Attachments table + Mission-A auto-migration

## Goal

Land the new `context_attachments` table, the Attachments view + bindings, and silently migrate Mission A's per-session `system_prompt` rows into session-scope attachment rows so the existing UI keeps working with no frontend change in this WP.

## Spec references

- Spec: §4 FR-201 / FR-202 / FR-203 / FR-204, §8 (Resolution at send time).
- Plan: § "Phase 3".

## Prerequisites

WP01 + WP02 merged.

## Subtasks

- **T001 — `core/attachments` package.** Attachment type (id ULID, scope_kind, scope_id, content_source, content, kind, position, created_at), Manager, Store interface, SQL-backed implementation. Test `core/attachments/attachments_test.go`.
- **T002 — Migration 308.** Creates `context_attachments` table with the schema from FR-201, then in the same transaction copies every row in `sessions` with non-empty `system_prompt` into `context_attachments` at session scope (`scope_kind='session', scope_id=session.id, content_source='inline:'+sha256(content), content=system_prompt, kind=context_kind, position=0`). Idempotent: re-runs after the first detect a row matching `(scope_kind='session', scope_id=<id>, content_source='inline:'+...)` and skip. Register migration 308 in the global registry.
- **T003 — RPC view.** `core/rpc/views/attachments/{api.go, impl.go, impl_test.go}` exposing List / ListResolved / Add / Remove / Reorder / Refresh. `Attachments_*` bindings in `core/rpc/bindings.go`.
- **T004 — Sessions shim.** `core/rpc/views/sessions/impl.go SetSystemPrompt` becomes a thin wrapper that calls the attachments manager — old callers (the existing NewSessionDialog code from Mission A) work unchanged. `Sessions.SystemPrompt` and `ContextKind` fields stay on the wire-shape Session for one release as a compatibility buffer.
- **T005 — `buildMessages` rewire.** `core/rpc/views/llm/impl.go buildMessages` reads `Attachments.ListResolved(sessionID)` instead of probing `SessionContextReader`. The `SessionContextReader` interface stays in place (deprecated comment) for the same one-release migration buffer; remove it next mission.

## Acceptance

- Mission A's existing flow (NewSessionDialog upload → system message in conversation) continues to work end-to-end with **zero frontend changes** in this WP.
- A pre-existing on-disk `<DataDir>/data.db` with one session having a `system_prompt` value migrates cleanly: post-WP03 query of `context_attachments` shows the corresponding row at session scope.
- `Attachments.ListResolved(sessionID)` returns the resolved list in declared order. (Project + global scopes will be empty for now — wired in WP04.)
- `go test -race -count=1 -short ./core/...` green; new attachments tests pass.

## Branch strategy

Branch `wp03-attachments-and-migration` off `main`, merge when WP03 acceptance gate passes.
