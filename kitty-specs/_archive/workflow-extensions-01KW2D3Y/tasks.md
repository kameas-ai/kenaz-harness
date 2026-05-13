# tasks.md — workflow-extensions-01KW2D3Y

## WP01 — Backend: run-history + cancel-run + next-fire RPC

**Objective**: Add three new `WorkflowsAPI` methods to expose per-workflow run history,
next scheduled fire time, and in-flight run cancellation.

**Files**:
- `core/workflows/scheduler/scheduler.go` — add `NextFire(workflowID string) (time.Time, error)` to `Scheduler` interface
- `core/workflows/scheduler/cron_scheduler.go` — implement `NextFire`
- `core/workflows/runtime.go` — add `Cancel(runID string) error` + `sync.Map` run registry
- `core/rpc/views/workflows/api.go` — add `ScheduleRunHistory`, `ScheduleNextFire`, `CancelRun` to `WorkflowsAPI` interface
- `core/rpc/views/workflows/impl.go` — implement the three methods
- `core/rpc/bindings.go` — wire `Workflows_ScheduleRunHistory`, `Workflows_ScheduleNextFire`, `Workflows_CancelRun`
- `core/rpc/views/workflows/impl_test.go` — unit tests

**Effort**: M (≈ 4h)

---

## WP02 — Settings → Workflows panel + graph editor

**Objective**: Expose workflow CRUD from Settings via a dedicated Workflows tab; provide
a visual DAG canvas editor so users can build workflows without editing raw YAML.

**Files**:
- `frontend/src/views/settings/WorkflowsSettingsPanel.vue` (new)
- `frontend/src/components/workflows/WorkflowGraphEditor.vue` (new)
- `frontend/src/views/settings/SettingsTabs.vue` — add Workflows tab entry
- `frontend/src/views/settings/SettingsView.vue` — add `activeTab === 'workflows'` branch
- `frontend/src/views/settings/__tests__/WorkflowsSettingsPanel.spec.ts` (new)
- `frontend/src/components/workflows/__tests__/WorkflowGraphEditor.spec.ts` (new)

**Effort**: L (≈ 8h)

---

## WP03 — Scheduled-runs inbox

**Objective**: Replace the "Runs" tab stub with a real scheduled-inbox view that lists upcoming
runs, recent history, run inspection, re-run, and in-flight cancel.

**Files**:
- `frontend/src/views/workflows/ScheduledInbox.vue` (new)
- `frontend/src/views/workflows/__tests__/ScheduledInbox.spec.ts` (new)
- `frontend/src/lib/workflowsClient.ts` — extend interface + bridge + fake with new methods
- `frontend/src/views/workflows/WorkflowsView.vue` — wire `ScheduledInbox` into Runs tab
- `frontend/src/views/workflows/__tests__/WorkflowsView.spec.ts` — update Runs tab tests

**Effort**: M (≈ 5h)
