# Spec: Cedar policy editor UI

**Status**: draft · **Owner**: alecfeeman

## 1. Why

Cedar policies live in `<DataDir>/policy/*.cedar`. Today users hand-edit those files in a text editor, which is fine for power users but excludes anyone non-technical from constraining the agent. The MCP-recipe install flow surfaces a `recommended_policy_template` — but no UI exists to install it.

## 2. Goals

- New `/policy` view (sibling of `/tools`, `/contexts`, `/memory`).
- Lists installed policies with their full text + last-modified.
- Editor pane with Cedar syntax highlighting (CodeMirror Cedar mode or fallback to plain text).
- Validate-on-save via the existing `cedar.NewEngine` parse path.
- "Install recommended template" button on recipe rows; copies the shipped template into `<DataDir>/policy/`.
- Live policy reload (no app restart) — the engine's `LoadFromDisk` already supports this.

## 3. Functional requirements

| ID | Requirement | Status |
|---|---|---|
| FR-001 | `core/rpc/views/policy/` API methods: List, Get, Save, Delete, Validate, InstallTemplate. | proposed |
| FR-002 | Frontend `PolicyView.vue` extension with editor + list. | proposed |
| FR-003 | Validation surfaces parse errors inline with line numbers. | proposed |
| FR-004 | InstallTemplate copies the recipe's `recommended_policy_template` from the shipped templates directory. | proposed |
| FR-005 | Save triggers a kernel-side reload signal so the engine picks up the new policy on next gate-call. | proposed |
| FR-006 | Audit log entry on every Save and Delete. | proposed |

## 4. Success criteria

- Non-technical user can install the filesystem-full recommended template through the UI without touching a terminal.
- Bad Cedar syntax is rejected on save with a clear error.
- Policy edits take effect on the next tool call within 1 s.
