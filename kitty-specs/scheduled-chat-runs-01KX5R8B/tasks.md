# Tasks — scheduled-chat-runs-01KX5R8B

## WP01 — SQLite schema + ScheduledChatStore

**Objective**: Land migration 0325 (two tables: `scheduled_chat_runs` +
`scheduled_chat_run_history`) and the `ScheduledChatStore` CRUD layer.

**Files**:
- `core/session/migrations_scheduled_chat_runs.go` (new) — migration 0325 DDL
- `core/session/migrations.go` — call `migration0325()` in `Migrations()` slice
- `core/scheduler/chat_store.go` (new) — `ScheduledChatStore` interface + SQLite impl

**Done when**: `go build ./core/...` clean; store CRUD round-trips in a
SQLite-in-memory test.

---

## WP02 — Scheduler job-kind extension + ChatRunDispatcher

**Objective**: Add `Kind` and `ChatRunSpec` to `scheduler.Job`; implement
`ChatRunDispatcher` that renders the prompt template, creates a headless
session, and routes to a configured output sink.

**Files**:
- `core/scheduler/job.go` — add `Kind string` (default `"session"`), `ChatRun *ChatRunSpec`
- `core/scheduler/chat_dispatcher.go` (new) — `ChatRunDispatcher` struct + `Dispatch()`
- `core/scheduler/chat_dispatcher_test.go` (new)

**Done when**: `go test -race -short ./core/scheduler/...` green with
dispatcher tests covering template render and sink routing.

---

## WP03 — Cedar action family

**Objective**: Add three Cedar action constants and one entity-type constant
to `core/policy/cedar/types.go`.

**Files**:
- `core/policy/cedar/types.go` — append:
  - `ActionScheduledRunCreate = "tool.scheduled_run.create"`
  - `ActionScheduledRunDelete = "tool.scheduled_run.delete"`
  - `ActionScheduledRunExecute = "tool.scheduled_run.execute"`
  - `EntityTypeScheduledChatRun = "ScheduledChatRun"`
  - `func ScheduledChatRunUID(id string) cedar.EntityUID`

**Done when**: `go build ./core/policy/...` clean; no other changes needed.

---

## WP04 — RPC view `scheduledchat` + bindings

**Objective**: `ScheduledChatAPI` interface, concrete impl, unit tests, and
Wails bindings.

**Files**:
- `core/rpc/views/scheduledchat/api.go` (new) — interface + wire types
- `core/rpc/views/scheduledchat/impl.go` (new) — concrete implementation
- `core/rpc/views/scheduledchat/impl_test.go` (new)
- `core/rpc/api.go` — add `ScheduledChat() scheduledchatview.ScheduledChatAPI`
  to `HarnessAPI` interface; field + accessor + wiring in `API.New`
- `core/rpc/bindings.go` — add `ScheduledChat_Create`, `ScheduledChat_Update`,
  `ScheduledChat_Delete`, `ScheduledChat_List`, `ScheduledChat_Get`,
  `ScheduledChat_RunNow`, `ScheduledChat_History`, `ScheduledChat_SetEnabled`

**Done when**: `go test -race -short ./core/rpc/...` green.

---

## WP05 — Frontend: Settings panel + client

**Objective**: `scheduledChatClient.ts` bridge, `ScheduledChatsPanel.vue`
list view, `ScheduledChatFormModal.vue` create/edit modal, Settings tab wiring.

**Files**:
- `frontend/src/lib/scheduledChatClient.ts` (new)
- `frontend/src/views/settings/scheduledchat/ScheduledChatsPanel.vue` (new)
- `frontend/src/views/settings/scheduledchat/ScheduledChatFormModal.vue` (new)
- `frontend/src/views/settings/SettingsTabs.vue` — add Scheduled Chats tab
- `frontend/src/views/settings/SettingsView.vue` — mount on `?tab=scheduledchats`
- `frontend/src/views/settings/scheduledchat/__tests__/ScheduledChatsPanel.spec.ts` (new)
- `frontend/src/views/settings/scheduledchat/__tests__/ScheduledChatFormModal.spec.ts` (new)

**Done when**: vitest green on new spec files.

---

## WP06 — ScheduledInbox extension

**Objective**: Extend `ScheduledInbox.vue` with a "Chat Runs" section that
lists scheduled chat run rows (with history accordion) below the workflow rows.

**Files**:
- `frontend/src/views/workflows/ScheduledInbox.vue` — add `chatClient` prop +
  second accordion section for chat runs
- `frontend/src/views/workflows/WorkflowsView.vue` — pass `chatClient` prop
- `frontend/src/views/workflows/__tests__/ScheduledInbox.spec.ts` — add tests
  for chat-run section rendering and empty state

**Done when**: vitest green; inbox renders both workflow and chat-run rows
when mocked data is supplied.

## Order

WP01 → WP02 → WP04 (sequential; each needs the previous).
WP03 can land any time before WP04.
WP05 → WP06 (WP06 needs the client from WP05).
