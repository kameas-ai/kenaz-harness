# v0.8.5 scope — workflow-extensions-01KW2D3Y

This note records the explicit v0.8.5 slice of the `workflow-extensions-01KW2D3Y` umbrella
mission and defers everything else to v0.10.0.

---

## In-scope for v0.8.5

### WP01 — Backend: run-history + cancel-run + next-fire RPC
**Objective**: Add `ScheduleRunHistory`, `ScheduleNextFire`, and `CancelRun` to `WorkflowsAPI`,
the scheduler interface, the workflow engine, and `core/rpc/bindings.go`.
**Effort**: M (~4 h)
**Spec refs**: FR-012, FR-013, FR-014

### WP02 — Settings → Workflows panel + `WorkflowGraphEditor`
**Objective**: Add a `?tab=workflows` entry to `SettingsTabs`, mount
`WorkflowsSettingsPanel.vue` with full CRUD (list / create / edit / delete / schedule),
and ship a minimal SVG-based drag-drop `WorkflowGraphEditor.vue` canvas.
**Effort**: L (~8 h)
**Spec refs**: FR-001 through FR-008

### WP03 — Scheduled-runs inbox
**Objective**: Replace the "Runs" tab stub in `WorkflowsView.vue` with
`ScheduledInbox.vue` — accordion rows per scheduled workflow, up to 20
recent runs each, re-run / inspect / cancel affordances, 30-second auto-refresh.
**Effort**: M (~5 h)
**Spec refs**: FR-009 through FR-016

---

## Deferred to v0.10.0

### DEFERRED-01 — `web_fetch` / `web_scrape` graph editor surface integration
**Objective**: Expose `web_fetch` and `web_scrape` step kinds in the graph editor node
palette (backend already ships these step runners from WP05).
**Effort**: S (~2 h)
**Spec ref**: FR-D01

### DEFERRED-02 — Visual workflow authoring agent (`/wf-author`)
**Objective**: `/wf-author` slash command that takes a natural-language description and
produces a YAML workflow via a `model_turn` chain, then opens it in the graph editor.
**Effort**: XL (~16 h, deserves its own mission)
**Spec ref**: FR-D02

### DEFERRED-03 — `sub_workflow` step kind
**Objective**: Backend engine support for invoking a named workflow as a single step of a
parent workflow, with bounded recursion depth.
**Effort**: L (~8 h)
**Spec ref**: FR-D03

### DEFERRED-04 — Human-in-the-loop (`human_review` step kind)
**Objective**: Pause a workflow run mid-execution and surface a UI review prompt;
resume or reject from the inbox.
**Effort**: XL (~16 h)
**Spec ref**: FR-D04

### DEFERRED-05 — Marketplace / remote registry
**Objective**: Remote workflow registry with version pinning + signature verification.
**Effort**: XL (requires server-side infra)
**Spec ref**: FR-D05

### DEFERRED-06 — `notify` step kind graph editor surface integration
**Objective**: Surface `notify` surface targets (OS, Slack, email, push) as form fields in
the graph editor's step config panel.
**Effort**: S (~2 h)
**Spec ref**: FR-D06
