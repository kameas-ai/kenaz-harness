---
work_package_id: "WP02"
title: "Projects entity + rail grouping"
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
phase: "Phase 2 — Projects entity"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-26T01:55:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP02 — Projects entity

## Goal

Land the new top-level Project entity, its CRUD surface, and rail grouping. No scoped attachments yet (that's WP03/WP04). Just project create/rename/delete and session membership.

## Spec references

- Spec: §3 (US3, US4, US5, US6), §4 FR-101 / FR-102 / FR-103 / FR-104.
- Plan: § "Phase 2".

## Prerequisites

WP01 merged. Storage consolidation v1 merged (provides the unified `data.db` for the new tables).

## Subtasks

- **T001 — `core/projects` package.** Project type (id ULID, name, description, created_at, updated_at), Manager, Store interface, SQL-backed implementation against the unified DB. Tests `core/projects/projects_test.go`.
- **T002 — Migrations 306 + 307.** Migration 306 adds `project_id TEXT NULL REFERENCES projects(id) ON DELETE SET NULL` to `sessions`. Migration 307 creates the `projects` table. Idempotent ALTER guard for SQLite. Register in the global migrations registry alongside 300-304.
- **T003 — RPC view.** `core/rpc/views/projects/{api.go, impl.go, impl_test.go}` exposing List / Get / Create / Rename / Delete (with `deleteSessions bool`) / AddSession / RemoveSession / ListSessions. Bindings `Projects_*` in `core/rpc/bindings.go`. New `Sessions.MoveToProject(sessionID, projectID *string)` on the existing SessionsAPI; new binding `Sessions_MoveToProject`.
- **T004 — Session.Record + manager.** Add `ProjectID *string` to `session.Record`; thread through Create/Get/List/sqlStore/memStore. New `Manager.SetProject(sessionID, projectID *string)`.
- **T005 — Frontend.**
  - `frontend/src/lib/types.ts` — Project type; `Session.projectId?: string`.
  - `frontend/src/lib/harnessClient.ts` — projects client + moveToProject method.
  - `frontend/src/views/projects/ProjectLandingPage.vue` — placeholder; project header + sessions list (no attachments yet).
  - `frontend/src/main.ts` — `/projects/:id` route.
  - `frontend/src/shell/LeftRail.vue` — group sessions by project header (collapsible). "Loose" group at the bottom for sessions with `projectId == null`. "+ project" button opens an inline name prompt. Right-click project header for Rename / Delete (with the cascade-delete-sessions opt-in modal).
  - `frontend/src/shell/NewSessionDialog.vue` — project dropdown above name; defaults to "(none)" / loose; an inline "+ New project" option creates one + selects it.

## Acceptance

- A1 (project CRUD persists across Wails restart).
- A6 + A7 (delete with / without sessions cascade).
- Sessions appear under their project header in the rail; loose sessions under "Loose".
- `go test -race -count=1 -short ./core/...` ≥ 742 + new project tests.
- Frontend tests + build green.

## Branch strategy

Branch `wp02-projects-entity` off `main`, merge when WP02 acceptance gate passes.
