# plan.md — workflow-extensions-01KW2D3Y

## Branch contract

| Field | Value |
|---|---|
| Branch | `release/v0.8.5` |
| Base | `main` |
| Merge gate | Green CI (Go + Vue test suites), ≥1 reviewer, smoke: Settings→Workflows panel renders, Scheduled Inbox tab shows history |
| Public Go API additions | `WorkflowsAPI.ScheduleRunHistory`, `WorkflowsAPI.ScheduleNextFire`, `WorkflowsAPI.CancelRun` |
| Feature flag | None (inherits `HARNESS_WORKFLOWS`). |

## Architecture

### 1. Backend (WP01)

Three new `WorkflowsAPI` methods implemented in `core/rpc/views/workflows/`:

```go
// ScheduleRunHistory returns up to limit recent run summaries for workflowID.
ScheduleRunHistory(ctx context.Context, workflowID string, limit int) ([]RunSummary, error)

// ScheduleNextFire returns the next cron fire time for workflowID.
// Returns zero Time + ErrSchedulerUnavailable when not scheduled.
ScheduleNextFire(ctx context.Context, workflowID string) (time.Time, error)

// CancelRun signals cancellation of an in-flight run. No-op when
// the run is already complete. Returns ErrRunNotFound when the
// run ID is unknown.
CancelRun(ctx context.Context, runID string) error
```

`ScheduleRunHistory` delegates to the existing `Scheduler.History(ctx, workflowID, limit)`.

`ScheduleNextFire` requires a new `NextFire(workflowID string) (time.Time, error)` method
on the `scheduler.Scheduler` interface (implemented on `CronScheduler` using
`robfig/cron.Entry().Next` + the `WithNext` functional option).

`CancelRun` requires a new `Engine.Cancel(runID string) error` method that looks up the
run's `context.CancelFunc` from an in-memory `sync.Map` keyed by run ID (populated at
`Engine.Run` entry and cleared on completion).

Wire all three into `core/rpc/bindings.go`:
```go
func (b *Bindings) Workflows_ScheduleRunHistory(workflowID string, limit int) ([]wv.RunSummary, error)
func (b *Bindings) Workflows_ScheduleNextFire(workflowID string) (string, error)  // ISO 8601 string
func (b *Bindings) Workflows_CancelRun(runID string) error
```

### 2. Frontend Settings panel (WP02)

New file: `frontend/src/views/settings/WorkflowsSettingsPanel.vue`

- Uses `workflowsClient.ts` (existing `WorkflowsClient` + catalog sub-client).
- Layout: toolbar row (New button + dropdown) above a table of installed workflows.
- Table columns: Name, Steps, Version, Source, Schedule badge, Actions (Edit/Delete/Schedule).
- "New" dropdown: "From template" → inline `SimpleTemplateEditor`; "From YAML" → inline `WorkflowEditor`.
- "Edit" → opens `WorkflowGraphEditor` in a full-width inline slot.
- "Delete" → confirmation `<dialog>` element (no external dependency).
- "Schedule" → `<dialog>` with cron input + timezone selector; calls `Workflows_ScheduleSet` /
  `Workflows_ScheduleClear`.

New file: `frontend/src/components/workflows/WorkflowGraphEditor.vue`

- Minimal DAG canvas built with SVG + Vue reactive data (no third-party graph lib).
- Props: `{ workflow: WorkflowsWorkflow | null, readonly?: boolean }`
- Emits: `{ save: WorkflowsWorkflow }`
- Internals:
  - Left panel: node palette (`model_turn`, `tool_call`, `shell`, `http_request`, `write_artifact`,
    `read_artifact`, `transform`, `conditional`, `web_fetch`, `web_scrape`, `notify`, `wait_until`,
    `aggregate`). Clicking a kind appends a new step.
  - Center: SVG canvas renders one rect per step in declaration order (sequential layout for v0.8.5;
    full DAG spatial layout deferred). Click-drag on step edges to draw `inputs_from` connections.
  - Right panel: step config form — name field + kind-specific fields rendered by a `<component :is>`
    dispatch keyed by step kind.
  - "View YAML" toggle: appends a read-only `<pre>` pane with serialized YAML.

`SettingsTabs.vue` gets a new entry:
```ts
{ to: '/settings?tab=workflows', label: 'Workflows', query: 'workflows' }
```

`SettingsView.vue` gets a new `v-else-if="activeTab === 'workflows'"` branch mounting
`WorkflowsSettingsPanel`.

### 3. Scheduled-runs inbox (WP03)

New file: `frontend/src/views/workflows/ScheduledInbox.vue`

- Props: `{ client: WorkflowsClient }` (same seam used by other workflow sub-views).
- Data: calls `client.scheduleList()` on mount to get all `ScheduleEntry` records.
  For each entry, calls `client.scheduleRunHistory(workflowId, 20)` in parallel.
  Also calls `client.scheduleNextFire(workflowId)` for the next-fire column.
- Auto-refresh: `setInterval(load, 30_000)` in `onMounted`; cleared in `onUnmounted`.
- Layout: per-workflow accordion row. Header: name + cron + next fire time + last status badge.
  Expanded body: table of recent runs (run ID, started, ended, status, error, Re-run / Inspect buttons).
- Cancel button on `running` rows calls `client.cancelRun(runId)`.

`workflowsClient.ts` extends `WorkflowsClient` with:
```ts
scheduleRunHistory(workflowId: string, limit: number): Promise<WorkflowsRunSummary[]>
scheduleNextFire(workflowId: string): Promise<string>  // ISO timestamp or "" if none
cancelRun(runId: string): Promise<void>
```
And extends `BridgeShape` + `createWorkflowsClient` accordingly.

`WorkflowsView.vue` "Runs" tab: replace stub paragraph with `<ScheduledInbox :client="client" />`.

## Dependency order

```
WP01 (backend) → WP02 (settings panel) → WP03 (scheduled inbox, which needs WP01's new RPCs)
```

WP02 and WP03 can be started once WP01 is merged. WP02 does not depend on WP03.
