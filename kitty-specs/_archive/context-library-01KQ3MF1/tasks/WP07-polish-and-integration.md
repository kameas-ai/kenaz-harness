---
work_package_id: "WP07"
title: "Drag-and-drop polish + integration acceptance suite"
dependencies:
  - "WP01"
  - "WP02"
  - "WP03"
  - "WP04"
  - "WP05"
  - "WP06"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
phase: "Phase 7 — Polish + integration"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-26T01:55:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP07 — Drag-and-drop polish + integration suite

## Goal

The "make it real" pass that closes the mission. Drag-and-drop session ↔ project in the rail, project landing page polish, end-to-end integration suite covering all 10 acceptance criteria.

## Spec references

- Spec: §10 (acceptance criteria A1–A10).
- Plan: § "Phase 7".

## Prerequisites

WP01–WP06 all merged.

## Subtasks

- **T001 — Drag-and-drop in rail.** `frontend/src/shell/LeftRail.vue` gets DnD: drag a session header onto a project header → moves the session into the project. Drag onto "Loose" → makes the session loose. Keyboard-accessible alternative: per-row "Move to project" menu (already exists from WP02).
- **T002 — Project landing polish.** `frontend/src/views/projects/ProjectLandingPage.vue` — full polish: editable description, project-scope memory count + link, sessions table with last-active timestamp, "Start a session in this project" CTA.
- **T003 — Backend integration test.** `core/contexts/integration_test.go` — end-to-end: create project, add 2 attachments at all 3 scopes, send a message, verify the full resolved-system-message order in the LLM request payload matches `[global1, global2, project1, project2, session1, session2, …history]`. Uses fake registry + scripted Stream from the existing test fixtures.
- **T004 — Frontend integration test.** `frontend/src/views/__tests__/integration.test.ts` — Vue-side smoke covering: create project → attach context at project scope → start session in project → resolved panel correct → memory pin at project scope visible from sister session.

## Acceptance

All 10 acceptance criteria from the spec pass:
- A1 (project persistence)
- A2 (project attachment applied)
- A3 (global attachment applied)
- A4 (memory promotion)
- A5 (global memory)
- A6 (delete project preserve sessions)
- A7 (delete project + sessions cascade)
- A8 (resolution panel order)
- A9 (path-traversal rejected)
- A10 (content snapshot survives library deletion)

Plus:
- The harness boot log shows the full migration sequence (305 → 306 → 307 → 308 → 309) on a freshly upgraded machine.
- `~/.kenaz/harness.log` records `context.attached`, `project.created`, `memory.scoped` events for the audit trail.
- `wails dev` reproduces the worked example: create project → attach context at project scope → start session → send message → resolved-context panel shows the attached file → assistant cites it.
- Documentation `docs/contexts.md` explains the scope hierarchy + project model + each phase's user-visible behaviour.

## Branch strategy

Branch `wp07-polish-and-integration` off `main`. Merge when all acceptance criteria pass + the documentation is in place. This commit closes the mission.
