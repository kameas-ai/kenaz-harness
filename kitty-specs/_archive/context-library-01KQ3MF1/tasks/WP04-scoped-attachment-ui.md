---
work_package_id: "WP04"
title: "Scoped attachment UI — global + project + resolved panel"
dependencies:
  - "WP01"
  - "WP02"
  - "WP03"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
phase: "Phase 4 — Scoped attachment UI"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-26T01:55:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP04 — Scoped attachment UI

## Goal

Surface the multi-scope attachment flow. Project landing page gets a "Project context" section; Settings gets a "Global context" section; SessionsView gets a collapsible "Resolved context" panel that shows the global → project → session order.

## Spec references

- Spec: §3 (US7 / US8 / US9 / US10 / US11), §6 (UI design — project landing page, Settings global context, NewSessionDialog hint).
- Plan: § "Phase 4".

## Prerequisites

WP01 / WP02 / WP03 merged.

## Subtasks

- **T001 — Shared components.**
  - `frontend/src/components/contexts/AttachmentRow.vue` — renders one attachment (icon, source path or inline-preview, scope badge, refresh, remove, reorder handle).
  - `frontend/src/components/contexts/ScopePicker.vue` — shows the active scope + lets the user pick global / project (when applicable) / session. Used inline in NewSessionDialog and on the project / settings landing pages.
  - `frontend/src/components/contexts/AttachmentTreePicker.vue` — embedded library tree for adding from the existing pool.
- **T002 — Project landing page.** `frontend/src/views/projects/ProjectLandingPage.vue` gains a "Project context" section: list (rendered via AttachmentRow), "+ Add context" button (opens AttachmentTreePicker), reorder via drag handles, "Refresh from source" affordance.
- **T003 — Settings → Global Context.** `frontend/src/views/settings/SettingsView.vue` gains a "Global context" section using the same shared components. Same UX as the project landing page but at global scope.
- **T004 — Resolved context panel.** `frontend/src/views/sessions/ResolvedContextPanel.vue` — collapsible panel above MessageList. Default collapsed. When expanded, shows three sub-sections (Global / Project / Session) listing the resolved attachments in order. Each row clickable for preview. Wire into `SessionsView.vue`.
- **T005 — NewSessionDialog scope hint.** When the user picks a project from the dropdown, show inline copy: "this session inherits the project's contexts. Files attached here go on the SESSION scope only — use the project page after creation for project-scope attachments."

## Acceptance

- A2 (project attachment applied to all sessions in the project, including future ones).
- A3 (global attachment applied everywhere).
- A8 (resolution panel renders global → project → session order correctly).
- A10 (content snapshot survives library file deletion — UI shows "source missing" warning + Detach affordance).
- `go test -race -count=1 -short ./core/...` ≥ baseline.
- Frontend tests + build green.

## Branch strategy

Branch `wp04-scoped-attachment-ui` off `main`, merge when WP04 acceptance gate passes.
