---
work_package_id: "WP01"
title: "Library content pool — file-tree CRUD + Contexts view"
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
phase: "Phase 1 — Library content pool"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-26T01:55:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP01 — Library content pool

## Goal

Land the file-tree CRUD that the existing `core/rpc/views/contextview` has stubbed. Pure file-system feature — no schema changes, no scope, no projects. Implements US1 + US2 in basic form (the in-place editor lands in WP05).

## Spec references

- Spec: §3 (US1, US2), §4 FR-001 / FR-002 / FR-003 / FR-004, §5 NFR-001 / NFR-003.
- Plan: § "Phase 1".

## Subtasks

- **T001 — `core/contexts` package.** Library struct with `Open(rootPath)`, `Tree() (Node, error)`, `Get(path) (string, error)`, `Save(path, content) error`, `CreateFolder(path) error`, `Rename(oldPath, newPath) error`, `Delete(path) error` (trash-style move to `.trash/<ts>-<name>`), `RecentlyApplied(limit) []string` (LRU JSON at `<root>/.recent.json`). Path validation: only `.md`, `.markdown`, `.txt`; reject `..`, absolute paths, symlink escape.
- **T002 — Watcher.** `core/contexts/watcher.go` fsnotify-backed watcher emitting `contexts:tree-changed` events when the root subtree mutates. 200 ms debounce. Linux/Darwin/Windows. Falls back to no-op if fsnotify isn't available; consumers can poll if they choose.
- **T003 — RPC view.** Replace the existing `core/rpc/views/contextview` stub with a real implementation. New file layout: `core/rpc/views/contexts/api.go` + `impl.go` + `impl_test.go`. The view accepts a `Library` and surfaces `List / Get / Save / CreateFolder / Rename / Delete / RecentlyApplied / RootPath`. Wire `Contexts_*` bindings in `core/rpc/bindings.go` + the matching stubs.
- **T004 — Frontend.** New `frontend/src/views/contexts/ContextsView.vue` rendering the three-column layout (tree / preview / recent). New `ContextTree.vue` component (lazy-render, virtualised when row count > 200). New `ContextPreview.vue` (markdown render, no editor yet). New `ContextRecent.vue` listing top-10 recents. Add the `/contexts` route in `frontend/src/main.ts`. Add the rail entry to `LeftRail.vue` between Bundles and Providers using the `FileText` icon.
- **T005 — Tests.** `core/contexts/library_test.go` covers: tree of 100 files in 50 folders ≤ 50 ms p99; path-traversal rejected; symlink-escape rejected; file >1 MiB rejected on Save; trash-move + cleanup; RecentlyApplied LRU. Frontend `__tests__/ContextsView.test.ts` covers: tree renders, preview shows file content, empty-state card with create/open-folder buttons.

## Out of scope (later WPs)

Scope assignment, projects, attachment table, NewSessionDialog integration, in-place editor, scoped memory.

## Acceptance

- A1 partially (library tree round-trip).
- A9 (path-traversal rejected) — full.
- `go test -race -count=1 -short ./core/contexts/...` passes.
- Sibling tests stay green (≥ 742 tests).
- Frontend tests + build remain green.
- Manual smoke: drop a `test.md` into `<DataDir>/contexts/`, see it in `/contexts`, click → preview rendered.

## Branch strategy

Worktree-isolated. Branch `wp01-context-library-pool` off `main`. Merge to `main` when WP01 acceptance gate passes.
