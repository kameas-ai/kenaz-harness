---
work_package_id: "WP05"
title: "In-place library editor + watcher polish"
dependencies:
  - "WP01"
  - "WP03"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
phase: "Phase 5 — Editor + watcher polish"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-26T01:55:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP05 — Editor + watcher polish

## Goal

In-place markdown editor on `/contexts`, plus correctness work for the fsnotify watcher (debounce, drop duplicates, race-handling). Can land in **parallel with WP04** since the editor/watcher don't touch attachment-scope concerns.

## Spec references

- Spec: §3 US3 / US4 / US5, §4 FR-009 / FR-010, §9 edge cases 7 / 8.
- Plan: § "Phase 5".

## Prerequisites

WP01 + WP03 merged. (WP02 not required.)

## Subtasks

- **T001 — Edit toggle in `ContextPreview.vue`.** Add an `Edit` button. Click → swap the rendered markdown for a textarea bound to the file's content. Footer with `Cancel` + `Save`. Save POSTs through `Contexts.Save` and re-fetches the tree (file size on disk, modified timestamp). No autosave — explicit only.
- **T002 — Watcher robustness.** `core/contexts/watcher.go` gets a 200 ms debounce + duplicate-event drop. Add `frontend/src/views/contexts/ContextsView.vue` listener for the `contexts:tree-changed` event; auto-refresh tree on receipt. Show a small "external change detected, refreshing…" toast.
- **T003 — Hidden files toggle + import flow.** `/contexts` page header gets a "Show hidden" toggle (dotfiles). Add an "Import file…" button that opens the OS file picker, reads the file via `FileReader`, and calls `Contexts.Save(targetPath, content)`.
- **T004 — Tests.** Vitest snapshot for the edit-mode rendering, e2e test that types into the textarea + clicks Save + reopens and shows the new content. `core/contexts/watcher_test.go` covers debounce + duplicate-drop + race scenarios.

## Acceptance

- A4 (edit → save → reopen shows new content).
- US5 (token chip in preview header — already from WP01).
- Watcher fires within 500 ms of an external write (with the 200 ms debounce baked in).
- Oversize files (>1 MiB) rejected on Save with a clear message.

## Branch strategy

Branch `wp05-editor-and-watcher-polish` off `main`, merge when WP05 acceptance gate passes.
