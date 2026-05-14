# Spec — Scheduled Chat Runs (`scheduled-chat-runs-01KX5R8B`)

**Status**: active · **Owner**: alecfeeman · **Target**: v0.10.0

## 1. Why

The harness already ships a cron scheduler (`core/scheduler/`, v0.3.0) and
a workflow-scheduler layer (`core/workflows/scheduler/`, v0.5.0).  What is
missing is the **chat-session equivalent**: a user saves a *prompt template*
with a cron expression; at trigger time the harness opens a fresh chat
session, runs the prompt, and surfaces the output through one of several
sinks (banner notification, file write, or email).

The canonical use case is the "daily EA briefing": every morning the
assistant pulls in calendar events, drafts a summary, and either shows
a banner or writes to a file — no user interaction required.

This mission is **distinct from workflow YAML schedules**: there is no
workflow definition to author.  The user writes a plain-language prompt
(with optional variable interpolation for `{{date}}`, `{{time}}`, etc.),
picks a target model, sets a cron expression, and chooses output sinks.

The feature reuses the existing `core/scheduler` primitives shipped in v0.3.0
(`kitty-specs/_archive/scheduler-01KQ309G/`) and extends the `Job.Spec`
envelope with a new `chat_run` kind.

## 2. Goals

| ID    | Goal |
|-------|------|
| G-001 | User can create a scheduled chat run (prompt template + cron + model + output sink) from Settings → Scheduled Chats. |
| G-002 | At the scheduled time the harness opens a headless chat session, runs the rendered prompt, and routes output to the configured sink(s). |
| G-003 | User can view recent run history, inspect output, pause/resume, and delete a scheduled chat run. |
| G-004 | The existing `ScheduledInbox.vue` (workflow-extensions-01KW2D3Y) is extended to surface chat-run rows alongside workflow rows under a unified "Scheduled" tab. |
| G-005 | All new backend is race-safe and covered by `-race -short` tests. |
| G-006 | All new Vue components pass `vue-tsc --noEmit` and have ≥80% Vitest branch coverage. |

## 3. Functional requirements

### 3.1 Data model

| ID     | Requirement |
|--------|-------------|
| FR-001 | New SQLite table `scheduled_chat_runs` (migration 0325) with columns: `id` (TEXT PK), `name` (TEXT), `prompt_template` (TEXT), `cron` (TEXT), `timezone` (TEXT DEFAULT ''), `model` (TEXT DEFAULT ''), `output_sink` (TEXT DEFAULT 'banner'), `enabled` (INTEGER DEFAULT 1), `created_at` (INTEGER), `updated_at` (INTEGER). |
| FR-002 | New SQLite table `scheduled_chat_run_history` (same migration) with columns: `id` (TEXT PK), `run_id` (TEXT NOT NULL), `chat_run_id` (TEXT REFERENCES scheduled_chat_runs ON DELETE CASCADE), `session_id` (TEXT), `status` (TEXT), `started_at` (INTEGER), `ended_at` (INTEGER), `output_snippet` (TEXT), `error` (TEXT). |
| FR-003 | `core/scheduler/job.go` extends `Job.Spec` to carry an optional `ChatRunSpec` sub-struct with `PromptTemplate string`, `Model string`, `OutputSink string`. The existing `session.Spec` continues to be the workflow-job path. |

### 3.2 Backend — scheduler extension

| ID     | Requirement |
|--------|-------------|
| FR-010 | `core/scheduler` Scheduler interface gains `Kind` awareness: `Upsert` accepts `Job` whose `Kind` field is either `"chat_run"` (new) or `"session"` (legacy, backward-compat). |
| FR-011 | When a `chat_run` job fires the scheduler calls `ChatRunDispatcher.Dispatch(ctx, job)` which: (a) renders the prompt template (substituting `{{date}}`, `{{time}}`, `{{cron_expr}}`); (b) creates a new session via `session.Manager`; (c) runs the rendered prompt through the chat runner (headless, no streaming to UI); (d) writes the result to the configured output sink. |
| FR-012 | `OutputSink` values: `"banner"` (Wails `runtime.EventsEmit("scheduled-chat:banner", payload)`), `"file:<path>"` (write UTF-8 text to path), `"none"` (silent, history-only). |
| FR-013 | Run history is persisted to `scheduled_chat_run_history` after each dispatch. |
| FR-014 | A `ScheduledChatStore` (new, `core/scheduler/chat_store.go`) implements SQLite-backed CRUD for `scheduled_chat_runs` and history queries. |

### 3.3 RPC surface

| ID     | Requirement |
|--------|-------------|
| FR-020 | New package `core/rpc/views/scheduledchat` with `ScheduledChatAPI` interface (separate from the Workflows surface to avoid further coupling). |
| FR-021 | `ScheduledChatAPI` methods: `Create(ctx, in CreateInput) (ChatRunEntry, error)`, `Update(ctx, in UpdateInput) (ChatRunEntry, error)`, `Delete(ctx, id string) error`, `List(ctx) ([]ChatRunEntry, error)`, `Get(ctx, id string) (ChatRunEntry, error)`, `RunNow(ctx, id string) (RunSummary, error)`, `History(ctx, id string, limit int) ([]RunSummary, error)`, `SetEnabled(ctx, id string, enabled bool) error`. |
| FR-022 | Wire `ScheduledChat_*` bindings in `core/rpc/bindings.go`. |
| FR-023 | Wire `ScheduledChat()` accessor on the `HarnessAPI` interface in `core/rpc/api.go`. |

### 3.4 Cedar action family

| ID     | Requirement |
|--------|-------------|
| FR-030 | Add Cedar action constants to `core/policy/cedar/types.go`: `ActionScheduledRunCreate = "tool.scheduled_run.create"`, `ActionScheduledRunDelete = "tool.scheduled_run.delete"`, `ActionScheduledRunExecute = "tool.scheduled_run.execute"`. |
| FR-031 | `EntityTypeScheduledChatRun = "ScheduledChatRun"` entity type. |
| FR-032 | `ScheduledChatRunUID(id string) cedar.EntityUID` helper. |
| FR-033 | Gate `Create` and `Delete` against the Cedar policy engine using `ActionScheduledRunCreate` / `ActionScheduledRunDelete`. Default-allow (permissive posture). |
| FR-034 | Gate dispatch execution against `ActionScheduledRunExecute` so policy authors can deny background execution entirely. |

### 3.5 Frontend

| ID     | Requirement |
|--------|-------------|
| FR-040 | Settings → Scheduled Chats tab (`?tab=scheduledchats`). Added to `SettingsTabs.vue` and `SettingsView.vue`. |
| FR-041 | `ScheduledChatsPanel.vue` — list view with create / edit / delete / enable-toggle for each scheduled chat run. Form fields: Name, Prompt Template (textarea), Cron Expression, Timezone, Target Model (optional, dropdown), Output Sink (banner / file / none), File Path (conditional). |
| FR-042 | `ScheduledChatFormModal.vue` — create / edit modal. Validates cron expression client-side (basic 5-field check). |
| FR-043 | Extend `ScheduledInbox.vue` (or its parent `WorkflowsView.vue`) to show a "Chat Runs" section below the workflow scheduled rows. Each row: name, cron, next-fire, last-run status, expand for history with output snippet. |
| FR-044 | New `scheduledChatClient.ts` in `frontend/src/lib/` that bridges to `ScheduledChat_*` Wails bindings. |
| FR-045 | Vitest tests for `ScheduledChatsPanel.vue` and `ScheduledChatFormModal.vue`. |

## 4. Non-functional requirements

| ID      | Requirement                                       | Threshold |
|---------|---------------------------------------------------|-----------|
| NFR-001 | `go build ./core/...` clean                       | Zero errors |
| NFR-002 | `go test -race -short ./core/scheduler/...`       | Green |
| NFR-003 | `go test -race -short ./core/rpc/...`             | Green |
| NFR-004 | Frontend vitest run                               | Green |
| NFR-005 | vue-tsc --noEmit on new .vue files                | Zero type errors |

## 5. Constraints

| ID    | Constraint |
|-------|------------|
| C-001 | DIRECTIVE_001: frontend talks to core only via `core/rpc`; no direct package imports. |
| C-002 | Headless chat dispatch must not block the main event loop; it runs in its own goroutine managed by the scheduler. |
| C-003 | File-sink writes are gated by the existing FSWrite Cedar policy; no new permission system needed. |
| C-004 | The new `core/scheduler` extension is backward-compatible: existing `Job` records with no `Kind` field are treated as `"session"` kind. |

## 6. Out of scope

- Email sink (requires SMTP/OAuth integration; tracked as post-v0.10.0).
- Variable interpolation beyond `{{date}}`, `{{time}}`, `{{cron_expr}}` (advanced templating is a separate mission).
- Multi-step chat runs (single-turn prompt only in this mission).
- UI for bulk-run history export.
