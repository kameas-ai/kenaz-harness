# Spec — Workflow Extensions (`workflow-extensions-01KW2D3Y`)

**Status**: active · **Owner**: alecfeeman · **Target**: v0.8.5 (catalog UI + scheduled inbox) / v0.10.0 (remainder)

## 1. Why

The `workflows-agentic-01KW2D3X` mission (v0.5.0) shipped a complete backend — YAML engine,
cron scheduler, catalog install, audit, Cedar gating, DAG execution. What it left as a stub
is the **user-facing surface for discovering, managing, and monitoring workflows without
touching YAML or the raw Library tab**.

Concretely today:
- The "Runs" tab in `WorkflowsView.vue` shows "Run history coming soon." — no scheduled-run
  inbox exists.
- There is no Settings → Workflows panel; workflows live entirely inside the Workflows main
  view which is not surfaced as a settings concern.
- The visual graph editor (`WorkflowGraphEditor`) does not exist; the only authoring path is
  hand-editing YAML in the `WorkflowEditor` textarea.

This umbrella mission (v0.8.5 scope) delivers **two** user-visible surfaces:

1. **Workflow Catalog UI** — Settings → Workflows panel: list user workflows, create/edit/delete,
   and a drag-drop graph editor (node palette + canvas) for visual authoring.
2. **Scheduled-runs inbox** — dedicated UI listing upcoming scheduled runs and recent run history
   per workflow, with inspect / re-run / cancel affordances.

## 2. Goals

| ID | Goal |
|---|---|
| G-001 | User can navigate to Settings → Workflows and see all installed user workflows. |
| G-002 | User can create a new workflow from the settings panel (template shortcut or YAML). |
| G-003 | User can edit a workflow via a drag-drop graph canvas — no YAML required for simple flows. |
| G-004 | User can delete a workflow from the settings panel. |
| G-005 | User can see scheduled-run state for each workflow (next fire time, last N runs). |
| G-006 | User can inspect run output, cancel an in-flight run, or manually re-run from the inbox. |
| G-007 | All new UI surfaces have Vitest component tests with ≥90% branch coverage. |

## 3. Functional requirements — v0.8.5 scope

### 3.1 Workflow Catalog UI (Settings panel)

| ID | Requirement |
|---|---|
| FR-001 | Settings → Workflows tab (query param `?tab=workflows`) lists all user-defined workflows. Columns: name, step count, source (builtin/user), version, actions. |
| FR-002 | "New" button opens a modal with two options: "From template" → `SimpleTemplateEditor`; "From YAML" → `WorkflowEditor`. |
| FR-003 | Each row has "Edit" (opens graph editor), "Delete" (confirmation dialog), and "Schedule" (opens schedule modal) buttons. |
| FR-004 | `WorkflowGraphEditor.vue` — a Vue component providing a visual DAG canvas. Node palette lists available step kinds. Drag a kind from palette onto the canvas to add a step. Connect steps with click-drag edges. Selected node opens a right-panel form for step-kind-specific fields. Save emits a structured workflow to the parent; parent routes through `client.save()`. |
| FR-005 | The graph editor reads an existing workflow (structured) as initial state so editing round-trips without YAML. |
| FR-006 | The graph editor has a "View YAML" toggle that opens a side-by-side YAML preview (read-only; same serialization path as `WorkflowEditor`). |
| FR-007 | Deleting a workflow with an active schedule shows a warning: "This workflow has an active cron schedule. Deleting it will also remove the schedule." |
| FR-008 | The Settings panel is reachable via the existing `SettingsTabs.vue` nav (adds `?tab=workflows` to the tab list). |

### 3.2 Scheduled-runs inbox

| ID | Requirement |
|---|---|
| FR-009 | `ScheduledInbox.vue` — a view component (or tab within `WorkflowsView`) listing every workflow that has a registered schedule. Each row shows: workflow name, cron expression, next-fire time (human-readable), last run status badge, last run timestamp. |
| FR-010 | Expanding a row reveals up to 20 recent runs in reverse-chronological order. Each run row: run ID, started/ended at, status (completed / failed / running), error message if failed. |
| FR-011 | "Re-run" button on any run row calls `Workflows_RunNow(workflowId)`. "Inspect" navigates to the session for that run in the main session list (if available). |
| FR-012 | "Cancel" button on a `running` status run (in-flight) calls a new `Workflows_CancelRun(runId)` RPC. Backend: adds `CancelRun(ctx, runId) error` to `WorkflowsAPI` interface + impl; delegates to `Engine.Cancel(runId)`. |
| FR-013 | Backend: `WorkflowsAPI.ScheduleRunHistory(ctx, workflowID string, limit int) ([]RunSummary, error)` — wraps `Scheduler.History`. Add to interface + impl + bindings. |
| FR-014 | Backend: `WorkflowsAPI.ScheduleNextFire(ctx, workflowID string) (time.Time, error)` — returns the next scheduled fire time for a workflow. Defers to `CronScheduler.NextFire(workflowID)` (new method on the scheduler interface). |
| FR-015 | The inbox auto-refreshes every 30 seconds via `setInterval`. Manual "Refresh" button forces an immediate reload. |
| FR-016 | Empty state: when no workflows have schedules, shows "No scheduled workflows. Open a workflow and set a cron schedule to see it here." |

## 4. Functional requirements — v0.10.0 scope (DEFERRED)

The following requirements are part of the umbrella mission but explicitly deferred from v0.8.5:

| ID | Requirement | Reason for deferral |
|---|---|---|
| FR-D01 | `web_fetch` / `web_scrape` step primitives in the graph editor palette | Already shipped in backend (WP05); deferring graph-editor surface integration until drag-drop canvas is stable |
| FR-D02 | Visual workflow authoring agent (`/wf-author` — natural-language → YAML) | Significant LLM-dependent surface; deserves its own mission |
| FR-D03 | `sub_workflow` step kind — invoke a named workflow as a step | Backend engine change; needs composition semantics spec |
| FR-D04 | Human-in-the-loop (`human_review` step kind) | Pausing execution + surface prompt is a non-trivial async pattern |
| FR-D05 | Marketplace — remote workflow registry with signature verification | Requires server-side infrastructure |
| FR-D06 | Comms natives — `notify` step kind surface integration in graph editor | Lower priority vs. core graph canvas |

## 5. Non-functional requirements

| ID | Requirement | Threshold |
|---|---|---|
| NFR-001 | Graph editor canvas render on 20-node workflow | < 100ms from data-load to interactive |
| NFR-002 | Scheduled inbox load time | < 500ms for ≤100 schedule entries + 20 history rows each |
| NFR-003 | All new Vue components pass `vue-tsc --noEmit` | Zero type errors on new files |
| NFR-004 | Component test coverage | ≥90% branch coverage on new .vue files |

## 6. Constraints

| ID | Constraint |
|---|---|
| C-001 | DIRECTIVE_001: frontend talks to core only via `core/rpc`; no direct package imports. |
| C-002 | No new Go dependencies for the drag-drop canvas: implement in pure Vue 3 + SVG/CSS transforms; no third-party graph libraries. |
| C-003 | The Settings → Workflows panel reuses the existing `workflowsClient.ts` types; no wire type duplication. |
| C-004 | Cancel run must not kill the host Wails process; it signals cancellation via the existing `context.CancelFunc` stored in the engine's run registry. |

## 7. Scope split (v0.8.5)

**WP01 — Backend: run-history + cancel-run + next-fire RPC surface** (FR-012 through FR-014)
Go-only. New RPC methods wired into `WorkflowsAPI`, `Bindings`, and the scheduler.

**WP02 — Settings → Workflows panel** (FR-001 through FR-008)
Frontend. `WorkflowsSettingsPanel.vue` + SettingsTabs entry + `WorkflowGraphEditor.vue` skeleton.

**WP03 — Scheduled-runs inbox** (FR-009 through FR-016)
Frontend. `ScheduledInbox.vue` replaces the "Runs" tab stub in `WorkflowsView.vue`.

## 8. Out of scope

Everything in §4 (FR-D01 through FR-D06). Also:
- A11y audit for the new panels (covered by `accessibility-audit` mission).
- Graph editor undo/redo stack (post-v0.8.5 polish).
- Graph editor multi-select / group operations.
